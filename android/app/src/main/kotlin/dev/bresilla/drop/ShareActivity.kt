package dev.bresilla.drop

import android.annotation.SuppressLint
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.view.ViewGroup
import android.webkit.WebChromeClient
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.FrameLayout
import androidx.activity.OnBackPressedCallback
import androidx.appcompat.app.AppCompatActivity
import java.io.DataOutputStream
import java.net.HttpURLConnection
import java.net.URL
import java.util.concurrent.Executors

/**
 * drop on a phone: the page the desktop already serves, in a window.
 *
 * There is deliberately no interface of its own. Everything a person sees here is the same HTML the
 * browser gets, from the same binary — so there is one set of screens to design, one to style, and
 * one to fix. What this adds is the part a page cannot do for itself: standing in Android's share
 * sheet, and handing over what was shared.
 */
class ShareActivity : AppCompatActivity() {

    private val work = Executors.newSingleThreadExecutor()
    private lateinit var page: WebView

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(saved: Bundle?) {
        super.onCreate(saved)

        page = WebView(this).apply {
            layoutParams = FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT,
            )
            settings.javaScriptEnabled = true
            settings.domStorageEnabled = true

            // Kept inside the app: the page only ever links to itself, and anything else is
            // somebody else's website, which belongs in a browser.
            webViewClient = object : WebViewClient() {
                override fun shouldOverrideUrlLoading(view: WebView, url: String): Boolean {
                    if (url.startsWith(at())) return false
                    startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)))
                    return true
                }
            }
            webChromeClient = WebChromeClient()
        }
        setContentView(page)

        // The page has its own way back — out of a path, out of a device — so the system button
        // follows it rather than closing the app from three levels in.
        onBackPressedDispatcher.addCallback(this, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() {
                if (page.canGoBack()) page.goBack() else finish()
            }
        })

        open(intent)
    }

    override fun onNewIntent(next: Intent) {
        super.onNewIntent(next)
        setIntent(next)
        open(next)
    }

    private fun open(from: Intent?) {
        if (at().isEmpty()) {
            askWhere()
            return
        }
        if (from?.action == Intent.ACTION_SEND) {
            handOver(from)
            return
        }
        page.loadUrl(at())
    }

    // ---------------------------------------------------------------- where drop is

    /**
     * The one thing the page cannot tell us, because it is what we need in order to load it.
     *
     * Asked for in a page of its own, styled from the same tokens as the real one, so the first
     * screen does not look like a different program from the second.
     */
    private fun askWhere() {
        page.addJavascriptInterface(object {
            @android.webkit.JavascriptInterface
            fun save(where: String) {
                val cleaned = where.trim().let { if (it.startsWith("http")) it else "http://$it" }
                remember(cleaned)
                runOnUiThread { open(intent) }
            }
        }, "android")

        page.loadUrl("file:///android_asset/setup.html")
    }

    // ---------------------------------------------------------------- handing something over

    /**
     * Posts what was shared to the bridge and follows it to the page, which then asks the one
     * question the share sheet cannot: who is it for.
     */
    private fun handOver(shared: Intent) {
        val text = shared.getStringExtra(Intent.EXTRA_TEXT)
        val stream = shared.getParcelableExtra<Uri>(Intent.EXTRA_STREAM)

        if (text.isNullOrBlank() && stream == null) {
            page.loadUrl(at())
            return
        }

        show(waiting())

        work.execute {
            try {
                val name = stream?.let { nameOf(it) }
                val bytes = stream?.let {
                    contentResolver.openInputStream(it)?.use { input -> input.readBytes() }
                }

                val to = post(text, name, bytes)
                runOnUiThread { page.loadUrl(to) }
            } catch (err: Exception) {
                runOnUiThread { show(failed(err.message)) }
            }
        }
    }

    /** Returns where the bridge said to go next. */
    private fun post(text: String?, name: String?, bytes: ByteArray?): String {
        val edge = "----drop" + System.nanoTime()

        val call = (URL(at().trimEnd('/') + "/share").openConnection() as HttpURLConnection).apply {
            requestMethod = "POST"
            doOutput = true
            instanceFollowRedirects = false
            connectTimeout = 8000
            readTimeout = 60000
            setRequestProperty("Content-Type", "multipart/form-data; boundary=$edge")
        }

        DataOutputStream(call.outputStream).use { out ->
            fun field(key: String, value: String) {
                out.writeBytes("--$edge\r\n")
                out.writeBytes("Content-Disposition: form-data; name=\"$key\"\r\n\r\n")
                out.write(value.toByteArray())
                out.writeBytes("\r\n")
            }

            if (!text.isNullOrBlank()) {
                // A bare url and a paragraph are different things on the far side, so both are sent
                // and the page decides which it is looking at.
                field(if (looksLikeLink(text)) "url" else "text", text.trim())
            }
            if (bytes != null) {
                out.writeBytes("--$edge\r\n")
                out.writeBytes("Content-Disposition: form-data; name=\"file\"; filename=\"${name ?: "shared"}\"\r\n")
                out.writeBytes("Content-Type: application/octet-stream\r\n\r\n")
                out.write(bytes)
                out.writeBytes("\r\n")
            }
            out.writeBytes("--$edge--\r\n")
        }

        // A redirect is the good answer: it is where the page will ask who this is for.
        val where = call.getHeaderField("Location")
        if (call.responseCode in 300..399 && !where.isNullOrEmpty()) {
            return URL(URL(at()), where).toString()
        }

        val body = (call.errorStream ?: call.inputStream)?.bufferedReader()?.use { it.readText() } ?: ""
        val why = Regex("\"error\"\\s*:\\s*\"([^\"]*)\"").find(body)?.groupValues?.get(1)
        throw Exception(why ?: "drop answered ${call.responseCode}")
    }

    // ---------------------------------------------------------------- odds and ends

    private fun nameOf(stream: Uri): String {
        contentResolver.query(stream, null, null, null, null)?.use { row ->
            val at = row.getColumnIndex(android.provider.OpenableColumns.DISPLAY_NAME)
            if (at >= 0 && row.moveToFirst()) row.getString(at)?.let { return it }
        }
        return stream.lastPathSegment ?: "shared"
    }

    private fun looksLikeLink(text: String): Boolean =
        !text.contains(Regex("\\s")) && (text.startsWith("http://") || text.startsWith("https://"))

    /** Two holding pages, in the same colours as the real one so nothing flashes a different skin. */
    private fun waiting() = page("sending…", "")

    private fun failed(why: String?) = page("could not reach drop", why ?: "")

    private fun page(title: String, detail: String) = """
        <!doctype html><meta name="viewport" content="width=device-width,initial-scale=1">
        <style>
          :root { color-scheme: light dark; }
          body { margin:0; height:100vh; display:grid; place-items:center; text-align:center;
                 font:16px/1.5 system-ui,sans-serif; background:#faf9fb; color:#1f1b26; padding:24px; }
          h1 { font-size:17px; font-weight:650; margin:0 0 8px; }
          p { margin:0; color:#6b6376; font-size:14px; overflow-wrap:anywhere; }
          @media (prefers-color-scheme: dark) { body { background:#121016; color:#e4dfeb; } p { color:#8f8799; } }
        </style>
        <div><h1>$title</h1><p>$detail</p></div>
    """.trimIndent()

    /**
     * loadData reads the string as a url, so a # starts a fragment and the rest of the page is
     * dropped. These pages are mostly hex colours, so they go in with a base url instead.
     */
    private fun show(html: String) =
        page.loadDataWithBaseURL(null, html, "text/html", "utf-8", null)

    private fun store() = getSharedPreferences("drop", Context.MODE_PRIVATE)
    private fun at(): String = store().getString("at", "") ?: ""
    private fun remember(where: String) = store().edit().putString("at", where).apply()

    override fun onDestroy() {
        work.shutdown()
        super.onDestroy()
    }
}

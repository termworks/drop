package dev.bresilla.drop

import android.app.AlertDialog
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.text.InputType
import android.view.Gravity
import android.view.ViewGroup
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AppCompatActivity
import java.util.concurrent.Executors

/**
 * The whole app: remember where the bridge is, and hand it whatever was shared.
 *
 * Launching it shows the address. Sharing to it asks which device and which path, then sends. The
 * phone holds no identity and pairs with nothing — the machine running `drop web` does that, and
 * this asks it to act.
 */
class ShareActivity : AppCompatActivity() {

    private val work = Executors.newSingleThreadExecutor()

    private lateinit var address: EditText
    private lateinit var status: TextView

    override fun onCreate(saved: Bundle?) {
        super.onCreate(saved)
        setContentView(screen())

        address.setText(remembered())

        // A share arrives as an intent rather than a tap, so it is handled here rather than waiting
        // for one.
        if (intent?.action == Intent.ACTION_SEND) {
            handOver(intent)
        }
    }

    // ---------------------------------------------------------------- the one screen

    private fun screen(): ViewGroup {
        val pad = (16 * resources.displayMetrics.density).toInt()

        val column = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(pad, pad, pad, pad)
        }

        column.addView(TextView(this).apply {
            text = "Where drop is running"
            textSize = 20f
        })

        column.addView(TextView(this).apply {
            text = "The address `drop web` printed, on the machine you want to reach."
            textSize = 14f
            alpha = 0.7f
            setPadding(0, pad / 2, 0, pad)
        })

        address = EditText(this).apply {
            hint = "http://100.68.8.159:47990"
            inputType = InputType.TYPE_TEXT_VARIATION_URI
            setSingleLine()
        }
        column.addView(address)

        column.addView(Button(this).apply {
            text = "Save and check"
            setOnClickListener { saveAndCheck() }
        })

        status = TextView(this).apply {
            setPadding(0, pad, 0, 0)
            gravity = Gravity.START
        }
        column.addView(status)

        column.addView(TextView(this).apply {
            text = "Then share anything to drop from another app."
            textSize = 14f
            alpha = 0.7f
            setPadding(0, pad, 0, 0)
        })

        return column
    }

    private fun saveAndCheck() {
        val at = address.text.toString().trim()
        if (at.isEmpty()) {
            status.text = "Give it an address first."
            return
        }

        remember(at)
        status.text = "asking…"

        work.execute {
            try {
                val found = Bridge(at).peers()
                onUi { status.text = "Reached it. ${found.size} device(s) paired there." }
            } catch (err: Exception) {
                onUi { status.text = "Could not reach it: ${err.message}" }
            }
        }
    }

    // ---------------------------------------------------------------- handing something over

    private fun handOver(shared: Intent) {
        val at = remembered()
        if (at.isEmpty()) {
            status.text = "Set the address first, then share again."
            return
        }

        val text = shared.getStringExtra(Intent.EXTRA_TEXT)
        val stream = shared.getParcelableExtra<Uri>(Intent.EXTRA_STREAM)

        if (text.isNullOrBlank() && stream == null) {
            finishWith("Nothing was shared.")
            return
        }

        status.text = "asking which device…"

        work.execute {
            try {
                val bridge = Bridge(at)
                val peers = bridge.peers().filter { it.paired }

                if (peers.isEmpty()) {
                    onUi { finishWith("Nothing is paired over there yet.") }
                    return@execute
                }
                onUi { chooseDevice(bridge, peers, text, stream) }
            } catch (err: Exception) {
                onUi { finishWith("Could not reach drop: ${err.message}") }
            }
        }
    }

    private fun chooseDevice(bridge: Bridge, peers: List<Peer>, text: String?, stream: Uri?) {
        val names = peers.map { it.name }.toTypedArray()

        // One device is not a choice, so it is not offered as one.
        if (names.size == 1) {
            choosePath(bridge, names[0], text, stream)
            return
        }

        AlertDialog.Builder(this)
            .setTitle("Send to")
            .setItems(names) { _, which -> choosePath(bridge, names[which], text, stream) }
            .setOnCancelListener { finish() }
            .show()
    }

    private fun choosePath(bridge: Bridge, peer: String, text: String?, stream: Uri?) {
        status.text = "asking what $peer shares…"

        work.execute {
            try {
                // Only what will actually take this: a terminal is shared with you but cannot be
                // sent to, and offering it would be offering a mistake.
                val wanted = if (stream != null) "files" else "chat"
                val spaces = bridge.spaces(peer).filter { it.writable && it.kind == wanted }

                if (spaces.isEmpty()) {
                    onUi { finishWith("$peer shares nothing you can send $wanted to.") }
                    return@execute
                }
                onUi {
                    if (spaces.size == 1) {
                        deliver(bridge, peer, spaces[0], text, stream)
                        return@onUi
                    }
                    val paths = spaces.map { it.path }.toTypedArray()
                    AlertDialog.Builder(this)
                        .setTitle("Where on $peer")
                        .setItems(paths) { _, which -> deliver(bridge, peer, spaces[which], text, stream) }
                        .setOnCancelListener { finish() }
                        .show()
                }
            } catch (err: Exception) {
                onUi { finishWith("Could not ask $peer: ${err.message}") }
            }
        }
    }

    private fun deliver(bridge: Bridge, peer: String, space: Space, text: String?, stream: Uri?) {
        status.text = "sending…"

        work.execute {
            try {
                if (stream != null) {
                    val bytes = contentResolver.openInputStream(stream)?.use { it.readBytes() }
                        ?: throw Exception("could not read what was shared")

                    bridge.send(peer, space.path, nameOf(stream), bytes)
                    onUi { finishWith("Sent to $peer${space.path}") }
                    return@execute
                }

                val body = text!!.trim()
                bridge.say(peer, body, looksLikeLink(body))
                onUi { finishWith("Sent to $peer") }
            } catch (err: Exception) {
                onUi { finishWith("Not sent: ${err.message}") }
            }
        }
    }

    // ---------------------------------------------------------------- odds and ends

    private fun nameOf(stream: Uri): String {
        contentResolver.query(stream, null, null, null, null)?.use { row ->
            val at = row.getColumnIndex(android.provider.OpenableColumns.DISPLAY_NAME)
            if (at >= 0 && row.moveToFirst()) {
                row.getString(at)?.let { return it }
            }
        }
        return stream.lastPathSegment ?: "shared"
    }

    private fun looksLikeLink(text: String): Boolean =
        !text.contains(Regex("\\s")) && (text.startsWith("http://") || text.startsWith("https://"))

    private fun finishWith(said: String) {
        Toast.makeText(this, said, Toast.LENGTH_LONG).show()
        status.text = said
        if (intent?.action == Intent.ACTION_SEND) finish()
    }

    private fun onUi(work: () -> Unit) = runOnUiThread(work)

    private fun store() = getSharedPreferences("drop", Context.MODE_PRIVATE)
    private fun remembered(): String = store().getString("at", "") ?: ""
    private fun remember(at: String) = store().edit().putString("at", at).apply()

    override fun onDestroy() {
        work.shutdown()
        super.onDestroy()
    }
}

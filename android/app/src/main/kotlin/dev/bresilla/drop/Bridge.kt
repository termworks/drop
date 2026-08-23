package dev.bresilla.drop

import org.json.JSONArray
import java.io.DataOutputStream
import java.io.InputStream
import java.net.HttpURLConnection
import java.net.URL

/** One device drop knows about. */
data class Peer(val name: String, val paired: Boolean)

/** One path a device shares with us. */
data class Space(val path: String, val kind: String, val writable: Boolean)

/**
 * Bridge talks to `drop web` over its own API.
 *
 * The phone is not a drop node: it has no identity and pairs with nothing. It asks a machine that
 * is one to send on its behalf, which is why the whole of this is four calls and no cryptography.
 */
class Bridge(private val at: String) {

    private fun open(path: String, method: String): HttpURLConnection {
        val url = URL(at.trimEnd('/') + path)
        return (url.openConnection() as HttpURLConnection).apply {
            requestMethod = method
            connectTimeout = 8000
            readTimeout = 20000
        }
    }

    private fun read(stream: InputStream): String = stream.bufferedReader().use { it.readText() }

    /** Fails with the reason the far end gave, rather than a status number nobody can act on. */
    private fun HttpURLConnection.orFail(): String {
        if (responseCode in 200..299) return read(inputStream)

        val body = errorStream?.let { read(it) } ?: ""
        val why = Regex("\"error\"\\s*:\\s*\"([^\"]*)\"").find(body)?.groupValues?.get(1)
        throw Exception(why ?: "the bridge answered $responseCode")
    }

    fun peers(): List<Peer> {
        val body = open("/api/peers", "GET").orFail()
        val items = JSONArray(body)

        return (0 until items.length()).map {
            val at = items.getJSONObject(it)
            Peer(at.getString("name"), at.optBoolean("paired"))
        }
    }

    fun spaces(peer: String): List<Space> {
        val body = open("/api/spaces/" + peer.encoded(), "GET").orFail()
        val items = JSONArray(body)

        return (0 until items.length()).map {
            val at = items.getJSONObject(it)
            Space(at.getString("path"), at.optString("kind"), at.optBoolean("writable"))
        }
    }

    /** Sends a message, as a link when it is one. */
    fun say(to: String, body: String, asLink: Boolean) {
        val payload = """{"to":${to.quoted()},"body":${body.quoted()},"kind":"${if (asLink) "link" else "text"}"}"""

        open("/api/say", "POST").apply {
            doOutput = true
            setRequestProperty("Content-Type", "application/json")
            outputStream.use { it.write(payload.toByteArray()) }
        }.orFail()
    }

    /** Sends a file into a path, as the browser's own upload does. */
    fun send(to: String, path: String, name: String, bytes: ByteArray) {
        val edge = "----drop" + System.nanoTime()

        open("/api/send", "POST").apply {
            doOutput = true
            setRequestProperty("Content-Type", "multipart/form-data; boundary=$edge")

            DataOutputStream(outputStream).use { out ->
                fun field(key: String, value: String) {
                    out.writeBytes("--$edge\r\n")
                    out.writeBytes("Content-Disposition: form-data; name=\"$key\"\r\n\r\n")
                    out.write(value.toByteArray())
                    out.writeBytes("\r\n")
                }

                field("to", to)
                field("path", path)

                out.writeBytes("--$edge\r\n")
                out.writeBytes("Content-Disposition: form-data; name=\"file\"; filename=\"$name\"\r\n")
                out.writeBytes("Content-Type: application/octet-stream\r\n\r\n")
                out.write(bytes)
                out.writeBytes("\r\n--$edge--\r\n")
            }
        }.orFail()
    }
}

private fun String.encoded(): String = java.net.URLEncoder.encode(this, "UTF-8")

/** Quotes a string for JSON, so a message with a quote or a newline in it survives the trip. */
private fun String.quoted(): String {
    val out = StringBuilder("\"")
    for (c in this) {
        when (c) {
            '"' -> out.append("\\\"")
            '\\' -> out.append("\\\\")
            '\n' -> out.append("\\n")
            '\r' -> out.append("\\r")
            '\t' -> out.append("\\t")
            else -> if (c < ' ') out.append("\\u%04x".format(c.code)) else out.append(c)
        }
    }
    return out.append("\"").toString()
}

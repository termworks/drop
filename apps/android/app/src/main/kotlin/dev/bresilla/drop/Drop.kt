package dev.bresilla.drop

import android.content.Context
import android.os.Build
import android.os.Environment
import java.io.File
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.withContext
import mobile.Events
import mobile.Mobile
import mobile.Node
import org.json.JSONArray
import org.json.JSONObject

/**
 * The node, for the whole process. The service starts it and keeps it; the screens read it.
 *
 * Everything here is a thin call into Go: the Kotlin side draws, and nothing it draws is decided here.
 */
object Drop {
    @Volatile
    var node: Node? = null
        private set

    private val _tick = MutableStateFlow(0)

    /** Bumped whenever something may have changed, so every screen on show reads itself again. */
    val tick: StateFlow<Int> = _tick.asStateFlow()

    private val _news = MutableSharedFlow<News>(extraBufferCapacity = 16)

    /** What happened that is worth saying out loud, once. */
    val news = _news.asSharedFlow()

    private val _moving = MutableStateFlow<Moving?>(null)
    val moving: StateFlow<Moving?> = _moving.asStateFlow()

    private val _trouble = MutableStateFlow<String?>(null)
    val trouble: StateFlow<String?> = _trouble.asStateFlow()

    /** Set by the service while a notification about a message would be noise: that chat is open. */
    @Volatile
    var watching: String? = null

    /** Where what arrives from other devices lands. */
    lateinit var downloads: File
        private set

    fun isReady(): Boolean = ::downloads.isInitialized

    var onSaid: ((from: String, text: String) -> Unit)? = null

    /** Told about a file that arrived: who from, what it is called, and where it is now. */
    var onLanded: ((from: String, name: String, at: File) -> Unit)? = null

    @Synchronized
    fun start(context: Context): Node {
        node?.let { return it }

        downloads = inbox(context)
        val files = context.filesDir
        // What this phone serves is a config the node starts from, written from the settings.
        Mobile.share(files.resolve("config").absolutePath, downloads.absolutePath, Settings.sharesFolder(context), Settings.folderWritable(context))
        val started = Mobile.start(
            files.resolve("config").absolutePath,
            files.resolve("data").absolutePath,
            downloads.absolutePath,
            Settings.name(context),
            events,
        )
        node = started
        bump()
        return started
    }

    @Synchronized
    fun stop() {
        node?.stop()
        node = null
        bump()
    }

    /** Starts the node again, for a change it reads only when it starts: its name, what it shares. */
    @Synchronized
    fun restart(context: Context) {
        stop()
        start(context)
    }

    fun bump() {
        _tick.value = _tick.value + 1
    }

    fun clearTrouble() {
        _trouble.value = null
    }

    /** Says the node itself could not be brought up, which every screen should show rather than wait on. */
    fun failed(text: String) {
        _trouble.value = text
    }

    /**
     * The public Downloads folder where the platform lets an app write there by path, and the app's
     * own corner of shared storage where it does not. Go puts a drop/ folder under whichever it is.
     */
    private fun inbox(context: Context): File {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            val shared = Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS)
            val mine = File(shared, "drop")
            if ((mine.isDirectory || mine.mkdirs()) && mine.canWrite()) return shared
        }
        return context.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS) ?: context.filesDir
    }

    private val events = object : Events {
        override fun changed() = bump()

        override fun said(from: String, text: String) {
            onSaid?.invoke(from, text)
            bump()
        }

        override fun paired(with: String) {
            _news.tryEmit(News.Paired(with))
            bump()
        }

        override fun trouble(text: String) {
            _trouble.value = text
        }

        override fun moving(name: String, done: Long, size: Long) {
            _moving.value = if (size in 1..done) null else Moving(name, done, size)
        }

        override fun landed(from: String, name: String, at: String) {
            onLanded?.invoke(from, name, File(at))
            bump()
        }
    }

    /** A Go call, off the main thread, with its failure as a value rather than a crash. */
    suspend fun <T> call(block: (Node) -> T): Result<T> = withContext(Dispatchers.IO) {
        val at = node ?: return@withContext Result.failure(IllegalStateException("drop is not running"))
        runCatching { block(at) }
    }

    suspend fun self(): Me? = call { Me.from(JSONObject(it.self())) }.getOrNull()

    suspend fun people(): Result<List<Person>> = call { node ->
        val all = JSONArray(node.people())
        List(all.length()) { Person.from(all.getJSONObject(it)) }
    }

    suspend fun paths(machine: String): Result<Paths> = call { Paths.from(JSONObject(it.paths(machine))) }

    suspend fun history(machine: String): Result<List<Said>> = call { node ->
        val all = JSONArray(node.history(machine))
        List(all.length()) { Said.from(all.getJSONObject(it)) }
    }

    suspend fun conversations(): Result<List<Chat>> = call { node ->
        val all = JSONArray(node.conversations())
        List(all.length()) { Chat.from(all.getJSONObject(it)) }
    }

    suspend fun managed(name: String): Result<Managed> = call { Managed.from(JSONObject(it.managed(name))) }

    suspend fun access(path: String): Result<Rule> = call { Rule.from(JSONObject(it.access(path))) }

    suspend fun knocked(): Result<List<Knock>> = call { node ->
        val all = JSONArray(node.knocked())
        List(all.length()) { Knock.from(all.getJSONObject(it)) }
    }

    suspend fun list(machine: String, path: String, dir: String): Result<List<Held>> = call { node ->
        val all = JSONArray(node.list(machine, path, dir))
        List(all.length()) { Held.from(all.getJSONObject(it)) }.sortedWith(compareBy({ !it.dir }, { it.name.lowercase() }))
    }
}

sealed interface News {
    data class Paired(val with: String) : News
}

data class Moving(val name: String, val done: Long, val size: Long) {
    val fraction: Float get() = if (size > 0) done.toFloat() / size else 0f
}

data class Me(val name: String, val id: String, val brief: String, val user: String) {
    companion object {
        fun from(o: JSONObject) = Me(o.optString("name"), o.optString("id"), o.optString("brief"), o.optString("user"))
    }
}

data class Machine(
    val name: String,
    val id: String,
    val brief: String,
    val paired: Boolean,
    val reaching: Boolean,
    val trusted: Boolean,
) {
    companion object {
        fun from(o: JSONObject) = Machine(
            o.optString("name"),
            o.optString("id"),
            o.optString("brief"),
            o.optBoolean("paired"),
            o.optBoolean("reaching"),
            o.optBoolean("trusted"),
        )
    }
}

data class Person(
    val name: String,
    val me: Boolean,
    val anon: Boolean,
    val trusted: Boolean,
    val machines: List<Machine>,
) {
    val reaching: Int get() = machines.count { it.reaching }

    companion object {
        fun from(o: JSONObject): Person {
            val machines = o.optJSONArray("machines") ?: JSONArray()
            return Person(
                o.optString("name"),
                o.optBoolean("me"),
                o.optBoolean("anon"),
                o.optBoolean("trusted"),
                List(machines.length()) { Machine.from(machines.getJSONObject(it)) },
            )
        }
    }
}

data class Served(
    val path: String,
    val archetype: String,
    val shape: String,
    val writable: Boolean,
    val locked: Boolean,
    val about: String,
) {
    /** What it behaves like: its own kind, or the kind it says it speaks like. */
    val kind: String get() = if (archetype in KNOWN) archetype else shape.ifEmpty { archetype }

    companion object {
        private val KNOWN = setOf("chat", "files", "share", "link", "note", "stream", "tty")

        fun from(o: JSONObject) = Served(
            o.optString("path"),
            o.optString("archetype"),
            o.optString("shape"),
            o.optBoolean("writable"),
            o.optBoolean("locked"),
            o.optString("about"),
        )
    }
}

data class Paths(val paths: List<Served>, val stale: String?) {
    companion object {
        fun from(o: JSONObject): Paths {
            val all = o.optJSONArray("paths") ?: JSONArray()
            val stale = o.optString("stale").ifEmpty { null }
            return Paths(List(all.length()) { Served.from(all.getJSONObject(it)) }, stale)
        }
    }
}

data class Said(
    val id: String,
    val out: Boolean,
    val kind: String,
    val body: String,
    val extra: String,
    val at: Long,
    val waiting: Boolean,
) {
    companion object {
        fun from(o: JSONObject) = Said(
            o.optString("id"),
            o.optBoolean("out"),
            o.optString("kind"),
            o.optString("body"),
            o.optString("extra"),
            o.optLong("at"),
            o.optBoolean("waiting"),
        )
    }
}

data class Held(val name: String, val size: Long, val at: Long, val dir: Boolean) {
    companion object {
        fun from(o: JSONObject) = Held(o.optString("name"), o.optLong("size"), o.optLong("at"), o.optBoolean("dir"))
    }
}

data class Chat(val machine: String, val last: Said, val arrivals: List<Long>) {
    companion object {
        fun from(o: JSONObject): Chat {
            val arrived = o.optJSONArray("in") ?: JSONArray()
            return Chat(
                o.optString("machine"),
                Said.from(o.optJSONObject("last") ?: JSONObject()),
                List(arrived.length()) { arrived.getLong(it) },
            )
        }
    }
}

data class Managed(
    val name: String,
    val person: String,
    val id: String,
    val machines: Int,
    val trusted: Boolean,
    val reaching: Boolean,
    val allowed: List<String>,
    val refused: List<String>,
) {
    companion object {
        fun from(o: JSONObject) = Managed(
            o.optString("name"),
            o.optString("person"),
            o.optString("id"),
            o.optInt("machines"),
            o.optBoolean("trusted"),
            o.optBoolean("reaching"),
            strings(o.optJSONArray("allowed")),
            strings(o.optJSONArray("refused")),
        )
    }
}

/** Somebody and how they stand with one of this phone's paths: "allowed", "refused", or "". */
data class Standing(val name: String, val person: Boolean, val machines: Int, val at: String, val inConfig: Boolean)

data class Asking(val who: String, val why: String, val `when`: String)

data class Rule(
    val path: String,
    val anyone: Boolean,
    val paired: Boolean,
    val password: Boolean,
    val who: List<Standing>,
    val asked: List<Asking>,
) {
    companion object {
        fun from(o: JSONObject): Rule {
            val who = o.optJSONArray("who") ?: JSONArray()
            val asked = o.optJSONArray("asked") ?: JSONArray()
            return Rule(
                o.optString("path"),
                o.optBoolean("anyone"),
                o.optBoolean("paired"),
                o.optBoolean("password"),
                List(who.length()) {
                    val w = who.getJSONObject(it)
                    Standing(w.optString("name"), w.optBoolean("person"), w.optInt("machines"), w.optString("at"), w.optBoolean("config"))
                },
                List(asked.length()) {
                    val a = asked.getJSONObject(it)
                    Asking(a.optString("who"), a.optString("why"), a.optString("when"))
                },
            )
        }
    }
}

data class Knock(val id: String, val brief: String, val at: Long, val asked: String, val why: String) {
    companion object {
        fun from(o: JSONObject) = Knock(o.optString("id"), o.optString("brief"), o.optLong("at"), o.optString("asked"), o.optString("why"))
    }
}

private fun strings(a: JSONArray?): List<String> = if (a == null) emptyList() else List(a.length()) { a.getString(it) }

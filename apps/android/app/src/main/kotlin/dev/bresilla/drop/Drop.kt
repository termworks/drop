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

    /** Told about a link somebody handed this phone to open. */
    var onLinked: ((from: String, url: String) -> Unit)? = null

    /** Whether the app is on screen, which is when Android lets a link be opened without asking. */
    @Volatile
    var visible = false

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

    /** Whether a code makes this phone somebody's, rather than pairing it with somebody. */
    fun owning(code: String): Boolean = code.trim().let { it.startsWith("drop://badge/") || it.startsWith("drop://key/") }

    /** Makes this phone one of somebody's machines from a code their computer showed, and starts the node again wearing it. */
    suspend fun take(context: Context, code: String): Result<String> = withContext(Dispatchers.IO) {
        runCatching { Mobile.take(code.trim()) }.onSuccess { restart(context) }
    }

    /** Whether a code is for joining a computer of yours as one of your machines. */
    fun machining(code: String): Boolean = code.trim().startsWith("drop://machine/")

    /**
     * Makes this phone one of the machines of whoever is showing a code, `drop machine add`, and starts
     * the node again wearing what it was given. A code from vouch or export still works.
     */
    suspend fun joinMachine(context: Context, code: String): Result<String> {
        val given = code.trim()
        if (owning(given)) return take(context, given)
        if (given.startsWith("drop://pair/")) {
            return Result.failure(IllegalArgumentException("That code pairs with a person. On your computer run drop machine add instead."))
        }
        return call { it.joinMachine(given) }
            .map { "This phone is one of your machines now, alongside $it. The rest of them hear about it within a few minutes." }
            .onSuccess { withContext(Dispatchers.IO) { restart(context) } }
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

        override fun linked(from: String, url: String) {
            onLinked?.invoke(from, url)
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

    suspend fun kept(): Result<List<Kept>> = call { node ->
        val all = JSONArray(node.kept())
        List(all.length()) { Kept.from(all.getJSONObject(it)) }
    }

    /** Every path on this phone (machine empty) or on a machine of yours, and who may reach each. */
    suspend fun levels(machine: String): Result<List<PathState>> = call { node ->
        val all = JSONArray(node.manage(machine, "list", "", "", "", false))
        List(all.length()) { PathState.from(all.getJSONObject(it)) }
    }

    /** Reads one path's permissions, or changes them: op is read, level, shown, allow, deny or unset. */
    suspend fun manage(
        machine: String,
        op: String,
        path: String,
        who: String = "",
        level: String = "",
        shown: Boolean = false,
    ): Result<PathDetail> = call { node ->
        PathDetail.from(JSONObject(node.manage(machine, op, path, who, level, shown)))
    }

    /** Takes this phone back out of your machines, and starts it again as its own. */
    suspend fun leave(context: Context): Result<Unit> = withContext(Dispatchers.IO) {
        runCatching { Mobile.leave() }.onSuccess { restart(context) }
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

/**
 * This device. Owner is whose it is, as a key fingerprint; signs says it holds that key, and when it
 * does not a machine that does vouched for it, until then.
 */
data class Me(
    val name: String,
    val id: String,
    val brief: String,
    val user: String,
    val owner: String,
    val signs: Boolean,
    val until: Long,
    val took: Boolean,
) {
    companion object {
        fun from(o: JSONObject) = Me(
            o.optString("name"),
            o.optString("id"),
            o.optString("brief"),
            o.optString("user"),
            o.optString("owner"),
            o.optBoolean("signs"),
            o.optLong("until"),
            o.optBoolean("took"),
        )
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
    /** What every machine holding it calls it; empty for what one machine holds alone. */
    val shared: String,
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
            o.optString("shared"),
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

/** A copy this phone keeps of something several machines hold, and where on the phone it is. */
data class Kept(val path: String, val archetype: String, val shared: String, val where: String) {
    companion object {
        fun from(o: JSONObject) = Kept(o.optString("path"), o.optString("archetype"), o.optString("shared"), o.optString("where"))
    }
}

/**
 * One path and who may reach it: the step it stands on (me, trusted, paired, anyone, or custom for a
 * rule the config wrote that is none of those), whether that was chosen here or by the config, and
 * who is let in or kept out beyond the step.
 */
data class PathState(
    val path: String,
    val archetype: String,
    val about: String,
    val level: String,
    val chosen: Boolean,
    val config: String,
    val shown: Boolean,
    val password: Boolean,
    val allowed: List<String>,
    val refused: List<String>,
    val asked: Int,
) {
    companion object {
        fun from(o: JSONObject) = PathState(
            o.optString("path"),
            o.optString("archetype"),
            o.optString("about"),
            o.optString("level"),
            o.optBoolean("chosen"),
            o.optString("config"),
            o.optBoolean("shown"),
            o.optBoolean("password"),
            strings(o.optJSONArray("allowed")),
            strings(o.optJSONArray("refused")),
            o.optInt("asked"),
        )
    }
}

/** A path's permissions with everybody who could be let in or kept out, and who is asking. */
data class PathDetail(val state: PathState, val who: List<WhoState>, val asking: List<Asking>) {
    companion object {
        fun from(o: JSONObject): PathDetail {
            val who = o.optJSONArray("who") ?: JSONArray()
            val asking = o.optJSONArray("asking") ?: JSONArray()
            return PathDetail(
                PathState.from(o),
                List(who.length()) { WhoState.from(who.getJSONObject(it)) },
                List(asking.length()) { asking.getJSONObject(it).let { a -> Asking(a.optString("who"), a.optString("why"), a.optString("when")) } },
            )
        }
    }
}

/** Somebody who could be let in or kept out: at is allowed, refused, or empty for left to the step. */
data class WhoState(val name: String, val person: Boolean, val trusted: Boolean, val at: String, val inConfig: Boolean) {
    companion object {
        fun from(o: JSONObject) = WhoState(o.optString("name"), o.optBoolean("person"), o.optBoolean("trusted"), o.optString("at"), o.optBoolean("inConfig"))
    }
}

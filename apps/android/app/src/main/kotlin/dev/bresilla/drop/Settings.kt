package dev.bresilla.drop

import android.content.Context
import android.os.Build

/** The few things this device remembers about itself, beyond what Go keeps. */
object Settings {
    private const val FILE = "drop"
    private const val NAME = "name"
    private const val FOLDER = "share.folder"
    private const val WRITABLE = "share.writable"
    private const val SEEN = "seen."
    private const val TERM_SIZE = "term.size"
    private const val MY_NAME = "me.name"

    private fun prefs(context: Context) = context.getSharedPreferences(FILE, Context.MODE_PRIVATE)

    /** What this device calls itself to others: chosen, or the phone's own model name. */
    fun name(context: Context): String {
        val chosen = prefs(context).getString(NAME, null)
        return chosen?.takeIf { it.isNotBlank() } ?: fallback()
    }

    fun rename(context: Context, name: String) {
        prefs(context).edit().putString(NAME, name.trim()).apply()
    }

    /** Whether paired devices may walk the folder where what arrives lands, and put things in it. */
    fun sharesFolder(context: Context): Boolean = prefs(context).getBoolean(FOLDER, false)

    fun folderWritable(context: Context): Boolean = prefs(context).getBoolean(WRITABLE, false)

    fun share(context: Context, folder: Boolean, writable: Boolean) {
        prefs(context).edit().putBoolean(FOLDER, folder).putBoolean(WRITABLE, writable).apply()
    }

    /** What you are called on this phone, in place of Me; empty is Me. */
    fun myName(context: Context): String = prefs(context).getString(MY_NAME, "") ?: ""

    fun nameMe(context: Context, name: String) {
        prefs(context).edit().putString(MY_NAME, name.trim()).apply()
    }

    /** What the list calls you: your name, or Me. */
    fun me(context: Context): String = myName(context).ifEmpty { "Me" }

    /** How big a terminal is drawn, in pixels of text, as it was last pinched to; zero before it ever was. */
    fun termSize(context: Context): Float = prefs(context).getFloat(TERM_SIZE, 0f)

    fun termSized(context: Context, size: Float) {
        prefs(context).edit().putFloat(TERM_SIZE, size).apply()
    }

    /** When a conversation was last looked at, which is what counts the rest as unread. */
    fun seen(context: Context, machine: String): Long = prefs(context).getLong(SEEN + machine, 0)

    fun saw(context: Context, machine: String) {
        prefs(context).edit().putLong(SEEN + machine, System.currentTimeMillis()).apply()
    }

    /** A name for an address book: one word, lower case, nothing a command line would trip on. */
    private fun fallback(): String {
        val model = Build.MODEL?.takeIf { it.isNotBlank() } ?: "android"
        return model.lowercase().replace(Regex("[^a-z0-9]+"), "-").trim('-').ifEmpty { "android" }
    }
}

package dev.bresilla.drop

import android.content.Context
import android.os.Build

/** The few things this device remembers about itself, beyond what Go keeps. */
object Settings {
    private const val FILE = "drop"
    private const val NAME = "name"

    /** What this device calls itself to others: chosen, or the phone's own model name. */
    fun name(context: Context): String {
        val chosen = context.getSharedPreferences(FILE, Context.MODE_PRIVATE).getString(NAME, null)
        return chosen?.takeIf { it.isNotBlank() } ?: fallback()
    }

    fun rename(context: Context, name: String) {
        context.getSharedPreferences(FILE, Context.MODE_PRIVATE).edit().putString(NAME, name.trim()).apply()
    }

    /** A name for an address book: one word, lower case, nothing a command line would trip on. */
    private fun fallback(): String {
        val model = Build.MODEL?.takeIf { it.isNotBlank() } ?: "android"
        return model.lowercase().replace(Regex("[^a-z0-9]+"), "-").trim('-').ifEmpty { "android" }
    }
}

package dev.bresilla.drop

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

/** Starts the node when the phone comes up, or when a new version of the app replaces the old one. */
class Boot : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        when (intent.action) {
            Intent.ACTION_BOOT_COMPLETED, Intent.ACTION_MY_PACKAGE_REPLACED -> NodeService.start(context)
        }
    }
}

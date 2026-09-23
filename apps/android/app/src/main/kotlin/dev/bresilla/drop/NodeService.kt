package dev.bresilla.drop

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.IBinder
import androidx.core.app.NotificationCompat
import mobile.Events
import mobile.Mobile
import mobile.Node

/**
 * Keeps the node up while the app is not in front.
 *
 * Android stops ordinary work when the app leaves the screen, and a node nobody can reach is not a
 * node. A foreground service is the only way to stay listening, and it costs a notification.
 */
class NodeService : Service() {
    private var node: Node? = null

    companion object {
        const val CHANNEL = "drop.node"
        const val NOTIFICATION = 1

        /** What the node last said, for the activity to draw. */
        @Volatile var status: String = "starting"
        @Volatile var address: String = ""
        @Volatile var id: String = ""

        fun start(context: Context) {
            val intent = Intent(context, NodeService::class.java)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                context.startForegroundService(intent)
            } else {
                context.startService(intent)
            }
        }

        fun stop(context: Context) {
            context.stopService(Intent(context, NodeService::class.java))
        }
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        makeChannel()
        startForeground(NOTIFICATION, notification("starting"))

        val events = object : Events {
            override fun onStatus(text: String) {
                status = text
                notify(text)
            }

            override fun onAddress(text: String) {
                address = text
            }
        }

        try {
            val started = Mobile.start(
                filesDir.resolve("config").absolutePath,
                filesDir.resolve("data").absolutePath,
                Build.MODEL ?: "android",
                events,
            )
            node = started
            id = started.brief()
            status = "reachable as ${started.brief()}"
            notify(status)
        } catch (e: Exception) {
            status = "did not start: ${e.message}"
            notify(status)
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int = START_STICKY

    override fun onDestroy() {
        node?.stop()
        node = null
        super.onDestroy()
    }

    private fun makeChannel() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return

        val channel = NotificationChannel(CHANNEL, "drop", NotificationManager.IMPORTANCE_LOW)
        channel.description = "Keeps this device reachable"
        getSystemService(NotificationManager::class.java).createNotificationChannel(channel)
    }

    private fun notification(text: String): Notification =
        NotificationCompat.Builder(this, CHANNEL)
            .setContentTitle("drop")
            .setContentText(text)
            .setSmallIcon(android.R.drawable.stat_sys_upload)
            .setOngoing(true)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .build()

    private fun notify(text: String) {
        getSystemService(NotificationManager::class.java).notify(NOTIFICATION, notification(text))
    }
}

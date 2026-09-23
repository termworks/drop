package dev.bresilla.drop

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import kotlin.concurrent.thread

/**
 * Keeps the node up while the app is not on screen.
 *
 * A node nobody can reach is not a node, so this is a foreground service: the price is a notification
 * that stays, and there is no way around that on Android since 8.
 */
class NodeService : Service() {
    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        channels(this)

        val running = Notification.Builder(this, QUIET)
            .setSmallIcon(R.drawable.ic_stat_drop)
            .setContentTitle("drop")
            .setContentText("reachable by the people you have paired with")
            .setContentIntent(opening(this, null))
            .setOngoing(true)
            .build()

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            startForeground(ONGOING, running, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        } else {
            startForeground(ONGOING, running)
        }

        Drop.onSaid = { from, text -> tell(this, from, text) }
        // Off the main thread: starting a node reads keys and binds sockets, which is long enough for
        // Android to call the app frozen.
        thread(name = "drop-start") { runCatching { Drop.start(applicationContext) } }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int = START_STICKY

    override fun onDestroy() {
        Drop.onSaid = null
        Drop.stop()
        super.onDestroy()
    }

    companion object {
        private const val QUIET = "drop.node"
        private const val LOUD = "drop.messages"
        private const val ONGOING = 1

        /** Which machine a notification opens, when it is about one. */
        const val MACHINE = "machine"

        fun start(context: Context) {
            context.startForegroundService(Intent(context, NodeService::class.java))
        }

        private fun channels(context: Context) {
            val manager = context.getSystemService(NotificationManager::class.java)
            manager.createNotificationChannel(
                NotificationChannel(QUIET, "Staying reachable", NotificationManager.IMPORTANCE_MIN),
            )
            manager.createNotificationChannel(
                NotificationChannel(LOUD, "Messages", NotificationManager.IMPORTANCE_HIGH),
            )
        }

        private fun opening(context: Context, machine: String?): PendingIntent {
            val intent = Intent(context, MainActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_CLEAR_TOP)
            if (machine != null) intent.putExtra(MACHINE, machine)
            return PendingIntent.getActivity(
                context,
                machine?.hashCode() ?: 0,
                intent,
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
            )
        }

        /** Says a message arrived, unless the conversation it belongs to is the one on screen. */
        private fun tell(context: Context, from: String, text: String) {
            if (Drop.watching == from) return

            val said = Notification.Builder(context, LOUD)
                .setSmallIcon(R.drawable.ic_stat_drop)
                .setContentTitle(from)
                .setContentText(text)
                .setStyle(Notification.BigTextStyle().bigText(text))
                .setContentIntent(opening(context, from))
                .setAutoCancel(true)
                .setCategory(Notification.CATEGORY_MESSAGE)
                .build()
            context.getSystemService(NotificationManager::class.java).notify(from.hashCode(), said)
        }
    }
}

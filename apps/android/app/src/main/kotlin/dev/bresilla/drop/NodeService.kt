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
import android.media.MediaScannerConnection
import android.os.IBinder
import android.webkit.MimeTypeMap
import androidx.core.content.FileProvider
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

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            startForeground(ONGOING, running, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        } else {
            startForeground(ONGOING, running)
        }

        Drop.onSaid = { from, text -> tell(this, from, text) }
        Drop.onLanded = { from, name, at -> handed(this, from, name, at) }
        Drop.onLinked = { from, url -> linked(this, from, url) }
        // Off the main thread: starting a node reads keys and binds sockets, which is long enough for
        // Android to call the app frozen.
        thread(name = "drop-start") {
            runCatching { Drop.start(applicationContext) }.onFailure { Drop.failed("drop could not start: ${it.message}") }
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int = START_STICKY

    override fun onDestroy() {
        Drop.onSaid = null
        Drop.onLanded = null
        Drop.onLinked = null
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

        /**
         * Opens a link somebody handed this phone. With the app on screen it opens at once; Android
         * lets nothing in the background start a browser, so otherwise it is a notification that
         * opens it when tapped. Only a web link: anything else would be a way to start apps.
         */
        private fun linked(context: Context, from: String, url: String) {
            if (!url.startsWith("https://") && !url.startsWith("http://")) return
            val view = Intent(Intent.ACTION_VIEW, android.net.Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            if (Drop.visible && runCatching { context.startActivity(view) }.isSuccess) return

            val pending = PendingIntent.getActivity(context, url.hashCode(), view, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
            val said = Notification.Builder(context, LOUD)
                .setSmallIcon(R.drawable.ic_stat_drop)
                .setContentTitle("$from sent a link")
                .setContentText(url)
                .setContentIntent(pending)
                .setAutoCancel(true)
                .setCategory(Notification.CATEGORY_MESSAGE)
                .build()
            context.getSystemService(NotificationManager::class.java).notify(url.hashCode(), said)
        }

        /** Says a file arrived: tapping it opens the conversation, and Open opens the file. */
        private fun handed(context: Context, from: String, name: String, at: java.io.File) {
            MediaScannerConnection.scanFile(context, arrayOf(at.absolutePath), null, null)

            val builder = Notification.Builder(context, LOUD)
                .setSmallIcon(R.drawable.ic_stat_drop)
                .setContentTitle(from)
                .setContentText("sent $name")
                .setContentIntent(opening(context, from))
                .setAutoCancel(true)
                .setCategory(Notification.CATEGORY_MESSAGE)

            if (at.isFile) {
                val uri = FileProvider.getUriForFile(context, "dev.bresilla.drop.files", at)
                val type = MimeTypeMap.getSingleton().getMimeTypeFromExtension(at.extension.lowercase()) ?: "*/*"
                val view = Intent(Intent.ACTION_VIEW).setDataAndType(uri, type).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                val pending = PendingIntent.getActivity(context, at.hashCode(), view, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
                builder.addAction(Notification.Action.Builder(null, "Open", pending).build())
            }
            context.getSystemService(NotificationManager::class.java).notify((from + name).hashCode(), builder.build())
        }
    }
}

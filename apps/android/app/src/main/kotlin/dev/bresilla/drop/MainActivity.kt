package dev.bresilla.drop

import android.Manifest
import android.app.Activity
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.Gravity
import android.view.ViewGroup
import android.widget.LinearLayout
import android.widget.TextView

/**
 * What this device is, and whether anybody can reach it.
 *
 * The screen is built in code rather than XML: there is one of them, and a layout file would be a
 * second place to look.
 */
class MainActivity : Activity() {
    private lateinit var name: TextView
    private lateinit var state: TextView
    private lateinit var where: TextView

    private val tick = Handler(Looper.getMainLooper())
    private val redraw = object : Runnable {
        override fun run() {
            draw()
            tick.postDelayed(this, 1000)
        }
    }

    override fun onCreate(saved: Bundle?) {
        super.onCreate(saved)
        setContentView(screen())
        askForNotifications()
        NodeService.start(this)
    }

    override fun onResume() {
        super.onResume()
        tick.post(redraw)
    }

    override fun onPause() {
        tick.removeCallbacks(redraw)
        super.onPause()
    }

    private fun screen(): ViewGroup {
        val pad = (24 * resources.displayMetrics.density).toInt()

        name = TextView(this).apply { textSize = 28f }
        state = TextView(this).apply { textSize = 16f; setPadding(0, pad / 2, 0, 0) }
        where = TextView(this).apply { textSize = 12f; setPadding(0, pad / 2, 0, 0) }

        return LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(pad, pad, pad, pad)
            addView(name)
            addView(state)
            addView(where)
        }
    }

    private fun draw() {
        name.text = if (NodeService.id.isEmpty()) "drop" else NodeService.id
        state.text = NodeService.status
        where.text = NodeService.address
    }

    /** Without this the foreground notification is silently dropped on Android 13 and later. */
    private fun askForNotifications() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) return

        val granted = checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS)
        if (granted != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 1)
        }
    }
}

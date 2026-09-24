package dev.bresilla.drop

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.runtime.mutableStateOf
import androidx.core.content.IntentCompat
import dev.bresilla.drop.ui.App
import dev.bresilla.drop.ui.Arrival
import dev.bresilla.drop.ui.DropTheme

class MainActivity : ComponentActivity() {
    private val arrival = mutableStateOf<Arrival?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 1)
        }
        NodeService.start(this)

        if (savedInstanceState == null) arrival.value = arrivalOf(intent)
        setContent {
            DropTheme {
                App(arrival = arrival.value, taken = { arrival.value = null })
            }
        }
    }

    override fun onResume() {
        super.onResume()
        Drop.visible = true
    }

    override fun onPause() {
        Drop.visible = false
        super.onPause()
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        arrival.value = arrivalOf(intent)
    }

    private fun arrivalOf(intent: Intent?): Arrival? {
        intent ?: return null
        intent.getStringExtra(NodeService.MACHINE)?.let { return Arrival.Open(it) }

        return when (intent.action) {
            Intent.ACTION_VIEW -> intent.dataString?.let { Arrival.Join(it) }
            // A file, some text — a link is text too — or both: a picture with a caption.
            Intent.ACTION_SEND -> {
                val file = IntentCompat.getParcelableExtra(intent, Intent.EXTRA_STREAM, Uri::class.java)
                val text = intent.getStringExtra(Intent.EXTRA_TEXT)?.takeIf { it.isNotBlank() }
                if (file == null && text == null) null else Arrival.Send(listOfNotNull(file), text)
            }
            Intent.ACTION_SEND_MULTIPLE ->
                IntentCompat.getParcelableArrayListExtra(intent, Intent.EXTRA_STREAM, Uri::class.java)
                    ?.takeIf { it.isNotEmpty() }
                    ?.let { Arrival.Send(it) }
            else -> null
        }
    }
}

package dev.bresilla.drop.ui

import android.content.Intent
import android.net.Uri
import android.os.PowerManager
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.ContentCopy
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import dev.bresilla.drop.Drop
import dev.bresilla.drop.Me
import dev.bresilla.drop.Settings
import java.io.File
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MeScreen(back: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var me by remember { mutableStateOf<Me?>(null) }
    var name by remember { mutableStateOf(Settings.name(context)) }
    var saved by remember { mutableStateOf(true) }

    LaunchedEffect(Unit) { me = Drop.self() }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("This device") },
                navigationIcon = { IconButton(onClick = back) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
            )
        },
    ) { pad ->
        LazyColumn(contentPadding = PaddingValues(top = pad.calculateTopPadding(), bottom = 32.dp)) {
            item {
                Column(Modifier.padding(20.dp)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Avatar(me?.name ?: name, size = 64.dp, online = me != null)
                    }
                    Spacer(Modifier.height(16.dp))
                    OutlinedTextField(
                        value = name,
                        onValueChange = { name = it; saved = false },
                        label = { Text("What others call this device") },
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth(),
                    )
                    Spacer(Modifier.height(10.dp))
                    Row(horizontalArrangement = Arrangement.spacedBy(10.dp), verticalAlignment = Alignment.CenterVertically) {
                        Button(enabled = !saved && name.isNotBlank(), onClick = {
                            Settings.rename(context, name)
                            saved = true
                            // The name is read when the node starts, so it starts again under it.
                            scope.launch {
                                withContext(Dispatchers.IO) {
                                    Drop.stop()
                                    Drop.start(context.applicationContext)
                                }
                                me = Drop.self()
                            }
                        }) { Text("Save") }
                        Text(
                            "Devices you pair with from now on see it.",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }
            item { Awake() }
            item { Section("Identity") }
            items(
                listOfNotNull(
                    me?.let { "Device" to it.id },
                    me?.user?.trim()?.takeIf { it.isNotEmpty() }?.let { "Person" to it },
                    if (Drop.isReady()) "Arrivals land in" to File(Drop.downloads, "drop").absolutePath else null,
                ),
            ) { (label, value) ->
                ListItem(
                    modifier = Modifier
                        .padding(horizontal = 12.dp, vertical = 3.dp)
                        .clip(RoundedCornerShape(18.dp))
                        .clickable { copy(context, label, value) },
                    colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
                    overlineContent = { Text(label) },
                    headlineContent = { Text(value, style = Mono, maxLines = 3, overflow = TextOverflow.Ellipsis) },
                    trailingContent = { Icon(Icons.Filled.ContentCopy, "Copy") },
                )
            }
            item {
                Text(
                    "The device key was made on this phone and never leaves it. Uninstalling drop takes it, and everybody would have to pair with this phone again.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(20.dp),
                )
            }
        }
    }
}

/**
 * Whether the phone lets drop keep its connections with the screen off. Left to itself, Android puts
 * an app to sleep after a while, and a node that is asleep is one nobody can reach.
 */
@Composable
private fun Awake() {
    val context = LocalContext.current
    val power = context.getSystemService(PowerManager::class.java)
    var awake by remember { mutableStateOf(power.isIgnoringBatteryOptimizations(context.packageName)) }

    // Asked again whenever this screen comes back, because the answer is given in the system's own.
    val owner = androidx.compose.ui.platform.LocalLifecycleOwner.current
    DisposableEffect(owner) {
        val watching = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME) awake = power.isIgnoringBatteryOptimizations(context.packageName)
        }
        owner.lifecycle.addObserver(watching)
        onDispose { owner.lifecycle.removeObserver(watching) }
    }

    Section("Staying reachable")
    ListItem(
        modifier = Modifier
            .padding(horizontal = 12.dp, vertical = 3.dp)
            .clip(RoundedCornerShape(18.dp))
            .clickable(enabled = !awake) {
                val ask = Intent(android.provider.Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS)
                    .setData(Uri.parse("package:${context.packageName}"))
                runCatching { context.startActivity(ask) }
            },
        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
        headlineContent = { Text(if (awake) "Reachable with the screen off" else "May be put to sleep") },
        supportingContent = {
            Text(
                if (awake) {
                    "Android lets drop keep its connections while the phone is idle."
                } else {
                    "Tap to let drop stay connected while the phone is idle, so messages and files arrive when they are sent."
                },
            )
        },
        trailingContent = { if (awake) Icon(Icons.Filled.CheckCircle, null, tint = MaterialTheme.colorScheme.tertiary) },
    )
}

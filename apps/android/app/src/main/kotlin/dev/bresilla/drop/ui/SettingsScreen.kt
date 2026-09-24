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
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.ContentCopy
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
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
import java.text.DateFormat
import java.util.Date
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** The phone itself: what it is called, whose it is, its download folder, and staying reachable. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(go: (Screen) -> Unit, back: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val tick by Drop.tick.collectAsState()
    var me by remember { mutableStateOf<Me?>(null) }
    var name by remember { mutableStateOf(Settings.name(context)) }
    var folder by remember { mutableStateOf(Settings.sharesFolder(context)) }
    var writable by remember { mutableStateOf(Settings.folderWritable(context)) }
    var restarting by remember { mutableStateOf(false) }
    var leaving by remember { mutableStateOf(false) }

    LaunchedEffect(tick) { me = Drop.self() ?: me }

    // What the node reads only when it starts — its name, what it shares — takes a start to change.
    val restart: (() -> Unit) -> Unit = { before ->
        before()
        restarting = true
        scope.launch {
            withContext(Dispatchers.IO) { Drop.restart(context.applicationContext) }
            me = Drop.self()
            restarting = false
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Settings") },
                navigationIcon = { IconButton(onClick = back) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
            )
        },
    ) { pad ->
        LazyColumn(contentPadding = PaddingValues(top = pad.calculateTopPadding(), bottom = 32.dp)) {
            item { Section("This phone") }
            item {
                Column(Modifier.padding(horizontal = 20.dp)) {
                    OutlinedTextField(
                        value = name,
                        onValueChange = { name = it.trim() },
                        label = { Text("Its name") },
                        supportingText = { Text("What your machines and the people you pair with see.") },
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth(),
                    )
                    Button(
                        enabled = !restarting && name.isNotBlank() && name != (me?.name ?: Settings.name(context)),
                        onClick = { restart { Settings.rename(context, name) } },
                    ) { Text("Rename") }
                }
            }

            item { Section("Whose phone this is") }
            me?.let { m ->
                item {
                    ListItem(
                        modifier = Modifier.padding(horizontal = 12.dp, vertical = 3.dp).clip(RoundedCornerShape(18.dp)),
                        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
                        overlineContent = { Text(m.owner, style = Mono, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                        headlineContent = {
                            Text(
                                when {
                                    !m.signs -> "One of your machines, vouched for by a computer"
                                    m.took -> "One of your machines, holding your key"
                                    else -> "Its own: nobody has made it theirs"
                                },
                            )
                        },
                        supportingContent = {
                            Text(
                                when {
                                    !m.signs -> "Until ${DateFormat.getDateInstance().format(Date(m.until))}, renewed whenever it reaches one of your computers."
                                    m.took -> "It signs for itself with the key it was given."
                                    else -> "Paths set to Only me are open to this phone alone."
                                },
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        },
                    )
                }
                item {
                    Row(Modifier.padding(horizontal = 20.dp, vertical = 6.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        if (!m.signs || m.took) {
                            OutlinedButton(onClick = { leaving = true }, enabled = !restarting) { Text("Leave") }
                        } else {
                            Button(onClick = { go(Screen.AddMachine) }) { Text("Make it one of my machines") }
                        }
                    }
                }
            }

            item { Section("Download folder") }
            item {
                Toggle(
                    title = "Share it with paired devices",
                    says = "Download/drop shows up as a path called phone, under My machines → this phone, where you choose who may open it.",
                    on = folder,
                    enabled = !restarting,
                ) { on -> restart { folder = on; writable = writable && on; Settings.share(context, folder, writable) } }
            }
            if (folder) {
                item {
                    Toggle(
                        title = "Let them put files in it",
                        says = "Without this they can only take. What they put in is written down in the conversation.",
                        on = writable,
                        enabled = !restarting,
                    ) { on -> restart { writable = on; Settings.share(context, true, on) } }
                }
            }

            item { Awake() }

            item { Section("Identity") }
            val rows = listOfNotNull(
                me?.let { "Device" to it.id },
                if (Drop.isReady()) "Arrivals land in" to File(Drop.downloads, "drop").absolutePath else null,
            )
            rows.forEach { (label, value) ->
                item {
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
            }
            item { Spacer(Modifier.height(8.dp)) }
        }
    }

    if (leaving) {
        AlertDialog(
            onDismissRequest = { leaving = false },
            title = { Text("Leave your machines?") },
            text = { Text("This phone goes back to being its own, with the key it had before. Your computers stop seeing it as yours, and paths set to Only me close to it.") },
            confirmButton = {
                TextButton(onClick = {
                    leaving = false
                    restarting = true
                    scope.launch {
                        Drop.leave(context.applicationContext)
                        me = Drop.self()
                        restarting = false
                    }
                }) { Text("Leave") }
            },
            dismissButton = { TextButton(onClick = { leaving = false }) { Text("Cancel") } },
        )
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
                if (awake) "Android lets drop keep its connections while the phone is idle."
                else "Tap to let drop stay connected while the phone is idle, so messages and files arrive when they are sent.",
            )
        },
        trailingContent = { if (awake) Icon(Icons.Filled.CheckCircle, null, tint = MaterialTheme.colorScheme.tertiary) },
    )
}

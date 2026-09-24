package dev.bresilla.drop.ui

import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.Chat
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.MoveToInbox
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
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
import androidx.compose.ui.unit.dp
import dev.bresilla.drop.Drop
import dev.bresilla.drop.Machine
import dev.bresilla.drop.Paths
import dev.bresilla.drop.Served
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MachineScreen(name: String, go: (Screen) -> Unit, back: () -> Unit, home: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val tick by Drop.tick.collectAsState()
    val said = remember { SnackbarHostState() }

    var machine by remember { mutableStateOf<Machine?>(null) }
    var paths by remember { mutableStateOf<Paths?>(null) }
    var failed by remember { mutableStateOf<String?>(null) }
    var asking by remember { mutableStateOf(true) }
    var menu by remember { mutableStateOf(false) }
    var forgetting by remember { mutableStateOf(false) }
    var linking by remember { mutableStateOf<Served?>(null) }
    var sendingTo by remember { mutableStateOf<Served?>(null) }
    var ringing by remember { mutableStateOf<Served?>(null) }

    Pulse()
    LaunchedEffect(tick) {
        machine = Drop.people().getOrNull()?.flatMap { it.machines }?.firstOrNull { it.name == name }
    }
    // Asked again as the screen refreshes: a machine that comes back, or one that was still writing
    // down a pairing made a moment ago, answers differently the second time.
    LaunchedEffect(name, tick) {
        if (paths == null) asking = true
        Drop.paths(name)
            .onSuccess { paths = it; failed = null }
            .onFailure { if (paths == null) failed = it.message }
        asking = false
    }

    val pick = rememberLauncherForActivityResult(ActivityResultContracts.GetMultipleContents()) { uris: List<Uri> ->
        val to = sendingTo ?: return@rememberLauncherForActivityResult
        if (uris.isEmpty()) return@rememberLauncherForActivityResult
        scope.launch {
            val staged = stage(context, uris)
            Drop.call { it.send(name, to.path, staged.joinToString("\n") { f -> f.absolutePath }) }
                .onSuccess { said.showSnackbar("Sent ${staged.size} to $name") }
                .onFailure { said.showSnackbar(it.message ?: "Could not send") }
            staged.forEach { it.delete() }
        }
    }

    val open: (Served) -> Unit = { s ->
        when {
            s.locked -> ringing = s
            s.kind == "chat" -> go(Screen.Chat(name))
            s.kind == "files" -> go(Screen.Files(name, s.path, "", s.writable))
            s.kind == "share" -> { sendingTo = s; pick.launch("*/*") }
            s.kind == "link" -> linking = s
            s.kind == "stream" || s.kind == "tty" -> go(Screen.Live(name, s.path, s.archetype, s.writable && s.kind == "tty"))
            s.kind == "note" -> go(Screen.Note(name, s.path))
            else -> scope.launch { said.showSnackbar("This build does not know how to open ${s.archetype}") }
        }
    }

    Scaffold(
        snackbarHost = { SnackbarHost(said) },
        topBar = {
            TopAppBar(
                title = {
                    Column {
                        Text(name)
                        Text(
                            (machine?.brief ?: "") + if (machine?.reaching == true) " · online" else "",
                            style = Mono,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                },
                navigationIcon = { IconButton(onClick = back) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
                actions = {
                    IconButton(onClick = { menu = true }) { Icon(Icons.Filled.MoreVert, "More") }
                    DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                        val trusted = machine?.trusted == true
                        DropdownMenuItem(
                            text = { Text(if (trusted) "Stop trusting" else "Trust") },
                            onClick = {
                                menu = false
                                scope.launch {
                                    Drop.call { it.trust(name, !trusted) }
                                    Drop.bump()
                                }
                            },
                        )
                        DropdownMenuItem(text = { Text("Manage") }, onClick = { menu = false; go(Screen.Manage(name)) })
                        DropdownMenuItem(text = { Text("Forget") }, onClick = { menu = false; forgetting = true })
                    }
                },
            )
        },
    ) { pad ->
        val all = paths?.paths ?: emptyList()
        LazyColumn(contentPadding = PaddingValues(top = pad.calculateTopPadding(), bottom = 32.dp)) {
            item { Header(name, machine, all, go, onSend = { s -> sendingTo = s; pick.launch("*/*") }) }
            paths?.stale?.let { item { Banner("$name is not answering right now, so this is what it shared last time.") } }
            failed?.let { item { Banner("Could not ask $name what it shares: $it", error = true) } }
            if (asking && paths == null) {
                item { Box(Modifier.fillMaxWidth().padding(32.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator() } }
            }
            if (all.isNotEmpty()) item { Section("Shared with you") }
            items(all, key = { it.path }) { s -> PathRow(s) { open(s) } }
            if (!asking && all.isEmpty() && failed == null) {
                item { Banner("$name shares nothing with you yet. Messages still work: anything you say waits until it can be delivered.") }
            }
            item { Transfer() }
        }
    }

    linking?.let { s ->
        var link by remember { mutableStateOf("https://") }
        AlertDialog(
            onDismissRequest = { linking = null },
            title = { Text("Open a link on $name") },
            text = { OutlinedTextField(link, { link = it }, singleLine = true, label = { Text("Link") }) },
            confirmButton = {
                TextButton(onClick = {
                    linking = null
                    scope.launch {
                        Drop.call { it.post(name, s.path, s.archetype, link.trim()) }
                            .onSuccess { said.showSnackbar("Sent to $name") }
                            .onFailure { said.showSnackbar(it.message ?: "Could not send") }
                    }
                }) { Text("Send") }
            },
            dismissButton = { TextButton(onClick = { linking = null }) { Text("Cancel") } },
        )
    }

    ringing?.let { s ->
        var why by remember { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { ringing = null },
            title = { Text("Ask for ${s.path.trimStart('/')}") },
            text = {
                Column {
                    Text("$name sees you can't open it yet. Asking lets them know, and they decide.")
                    Spacer(Modifier.height(12.dp))
                    OutlinedTextField(why, { why = it }, label = { Text("Why (optional)") }, singleLine = true)
                }
            },
            confirmButton = {
                TextButton(onClick = {
                    ringing = null
                    scope.launch {
                        Drop.call { it.askFor(name, s.path, why.trim()) }
                            .onSuccess { said.showSnackbar("Asked $name") }
                            .onFailure { said.showSnackbar(it.message ?: "Could not ask") }
                    }
                }) { Text("Ask") }
            },
            dismissButton = { TextButton(onClick = { ringing = null }) { Text("Cancel") } },
        )
    }

    if (forgetting) {
        AlertDialog(
            onDismissRequest = { forgetting = false },
            title = { Text("Forget $name?") },
            text = { Text("It arrives as a stranger from then on, and you would have to pair again to reach it.") },
            confirmButton = {
                TextButton(onClick = {
                    forgetting = false
                    scope.launch {
                        Drop.call { it.forget(name) }
                        home()
                    }
                }) { Text("Forget") }
            },
            dismissButton = { TextButton(onClick = { forgetting = false }) { Text("Cancel") } },
        )
    }
}

/** The two things done most: talk, and hand something over. */
@Composable
private fun Header(name: String, machine: Machine?, all: List<Served>, go: (Screen) -> Unit, onSend: (Served) -> Unit) {
    Column(Modifier.padding(horizontal = 20.dp, vertical = 12.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Avatar(name, size = 64.dp, online = machine?.reaching)
            Spacer(Modifier.width(16.dp))
            Column {
                Text(name, style = MaterialTheme.typography.headlineSmall)
                Text(
                    when {
                        machine == null -> ""
                        machine.reaching -> "connected now"
                        else -> "will get what you send when it is back"
                    },
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        Spacer(Modifier.height(16.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(10.dp), modifier = Modifier.fillMaxWidth()) {
            val inbox = all.firstOrNull { it.kind == "share" && !it.locked }
            FilledTonalButton(
                onClick = { go(Screen.Chat(name)) },
                contentPadding = PaddingValues(horizontal = 12.dp),
                modifier = Modifier.weight(1f),
            ) {
                Icon(Icons.AutoMirrored.Filled.Chat, null, Modifier.size(18.dp))
                Spacer(Modifier.width(6.dp))
                Text("Message", maxLines = 1)
            }
            if (inbox != null) {
                FilledTonalButton(
                    onClick = { onSend(inbox) },
                    contentPadding = PaddingValues(horizontal = 12.dp),
                    modifier = Modifier.weight(1f),
                ) {
                    Icon(Icons.Filled.MoveToInbox, null, Modifier.size(18.dp))
                    Spacer(Modifier.width(6.dp))
                    Text("Send files", maxLines = 1)
                }
            }
        }
    }
}

@Composable
private fun PathRow(s: Served, onClick: () -> Unit) {
    ListItem(
        modifier = Modifier
            .padding(horizontal = 12.dp, vertical = 3.dp)
            .clip(RoundedCornerShape(20.dp))
            .clickable(onClick = onClick),
        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
        leadingContent = { KindBadge(s.kind) },
        headlineContent = { Text(s.path.trimStart('/').ifEmpty { "/" }, style = MaterialTheme.typography.titleMedium) },
        supportingContent = {
            Text(
                s.about.ifEmpty { s.archetype },
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2,
            )
        },
        trailingContent = {
            when {
                s.locked -> Icon(Icons.Filled.Lock, "locked", tint = MaterialTheme.colorScheme.outline)
                else -> Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, look(s.kind).verb, tint = MaterialTheme.colorScheme.outline)
            }
        },
    )
}

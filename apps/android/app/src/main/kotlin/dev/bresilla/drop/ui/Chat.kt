package dev.bresilla.drop.ui

import android.app.NotificationManager
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
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.InsertDriveFile
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.AttachFile
import androidx.compose.material.icons.filled.Done
import androidx.compose.material.icons.filled.Schedule
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledIconButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.unit.dp
import dev.bresilla.drop.Drop
import dev.bresilla.drop.Said
import dev.bresilla.drop.Served
import dev.bresilla.drop.Settings
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ChatScreen(machine: String, go: (Screen) -> Unit, back: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val tick by Drop.tick.collectAsState()
    val said = remember { SnackbarHostState() }
    val list = rememberLazyListState()

    var talk by remember { mutableStateOf<List<Said>>(emptyList()) }
    var online by remember { mutableStateOf(false) }
    var inbox by remember { mutableStateOf<Served?>(null) }
    var draft by rememberSaveable { mutableStateOf("") }

    // While this conversation is on screen, a message in it is not news.
    DisposableEffect(machine) {
        Drop.watching = machine
        context.getSystemService(NotificationManager::class.java).cancel(machine.hashCode())
        onDispose { if (Drop.watching == machine) Drop.watching = null }
    }
    Pulse()
    LaunchedEffect(tick) {
        Drop.history(machine).onSuccess { talk = it }
        online = Drop.people().getOrNull()?.flatMap { it.machines }?.firstOrNull { it.name == machine }?.reaching == true
    }
    LaunchedEffect(machine) {
        Drop.call { it.deliver(machine) }
        inbox = Drop.paths(machine).getOrNull()?.paths?.firstOrNull { it.kind == "share" && !it.locked }
    }
    // What is on screen has been read, which is what the count of unread is kept against.
    LaunchedEffect(talk.size) {
        Settings.saw(context, machine)
        if (talk.isNotEmpty()) list.animateScrollToItem(0)
    }

    val pick = rememberLauncherForActivityResult(ActivityResultContracts.GetMultipleContents()) { uris: List<Uri> ->
        val to = inbox ?: return@rememberLauncherForActivityResult
        if (uris.isEmpty()) return@rememberLauncherForActivityResult
        scope.launch {
            val staged = stage(context, uris)
            Drop.call { it.send(machine, to.path, staged.joinToString("\n") { f -> f.absolutePath }) }
                .onFailure { said.showSnackbar(it.message ?: "Could not send") }
            staged.forEach { it.delete() }
        }
    }

    Scaffold(
        snackbarHost = { SnackbarHost(said) },
        topBar = {
            TopAppBar(
                title = {
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        modifier = Modifier.clickable { go(Screen.Machine(machine)) },
                    ) {
                        Avatar(machine, size = 36.dp, online = online)
                        Spacer(Modifier.width(12.dp))
                        Column {
                            Text(machine, style = MaterialTheme.typography.titleMedium)
                            Text(
                                if (online) "online" else "messages wait until it is back",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                },
                navigationIcon = { IconButton(onClick = back) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
            )
        },
        bottomBar = {
            Composer(
                draft = draft,
                onDraft = { draft = it },
                canAttach = inbox != null,
                onAttach = { pick.launch("*/*") },
                onSend = {
                    val text = draft.trim()
                    if (text.isNotEmpty()) {
                        draft = ""
                        scope.launch {
                            Drop.call { it.say(machine, text) }.onFailure { said.showSnackbar(it.message ?: "Could not send") }
                        }
                    }
                },
            )
        },
    ) { pad ->
        Column(Modifier.fillMaxSize().padding(pad)) {
            Transfer()
            if (talk.isEmpty()) {
                Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    Text(
                        "Say something to $machine.\nIt is kept on both devices, and nowhere else.",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        textAlign = androidx.compose.ui.text.style.TextAlign.Center,
                    )
                }
            } else {
                LazyColumn(
                    state = list,
                    reverseLayout = true,
                    contentPadding = PaddingValues(horizontal = 12.dp, vertical = 8.dp),
                    verticalArrangement = Arrangement.spacedBy(4.dp, Alignment.Bottom),
                    modifier = Modifier.fillMaxSize(),
                ) {
                    items(talk.asReversed(), key = { it.id }) { Bubble(it) }
                }
            }
        }
    }
}

private val clock = SimpleDateFormat("HH:mm", Locale.getDefault())

@Composable
private fun Bubble(m: Said) {
    val context = LocalContext.current
    val scheme = MaterialTheme.colorScheme

    if (m.kind == "event") {
        Text(
            m.body,
            style = MaterialTheme.typography.labelSmall,
            color = scheme.onSurfaceVariant,
            modifier = Modifier.fillMaxWidth().padding(vertical = 6.dp),
            textAlign = androidx.compose.ui.text.style.TextAlign.Center,
        )
        return
    }

    val widest = (LocalConfiguration.current.screenWidthDp * 0.78f).dp
    val shape = if (m.out) {
        RoundedCornerShape(topStart = 20.dp, topEnd = 20.dp, bottomStart = 20.dp, bottomEnd = 6.dp)
    } else {
        RoundedCornerShape(topStart = 20.dp, topEnd = 20.dp, bottomStart = 6.dp, bottomEnd = 20.dp)
    }
    val ground = if (m.out) scheme.primary else scheme.surfaceContainerHigh
    val ink = if (m.out) scheme.onPrimary else scheme.onSurface
    val file = if (m.kind == "file" && !m.out) landed(m.body) else null

    Row(Modifier.fillMaxWidth(), horizontalArrangement = if (m.out) Arrangement.End else Arrangement.Start) {
        Surface(
            color = ground,
            contentColor = ink,
            shape = shape,
            modifier = Modifier
                .widthIn(max = widest)
                .clickable(enabled = file != null || m.kind == "link") {
                    when {
                        file != null -> open(context, file)
                        m.kind == "link" -> browse(context, m.body)
                    }
                },
        ) {
            Column(Modifier.padding(horizontal = 14.dp, vertical = 9.dp)) {
                when (m.kind) {
                    "file" -> Row(verticalAlignment = Alignment.CenterVertically) {
                        Icon(Icons.AutoMirrored.Filled.InsertDriveFile, null, Modifier.size(28.dp))
                        Spacer(Modifier.width(10.dp))
                        Column {
                            Text(m.body, style = MaterialTheme.typography.titleSmall)
                            Text(
                                listOfNotNull(m.extra.ifEmpty { null }, if (file != null) "tap to open" else null).joinToString(" · "),
                                style = MaterialTheme.typography.bodySmall,
                                color = ink.copy(alpha = 0.75f),
                            )
                        }
                    }
                    "link" -> Text(m.body, textDecoration = TextDecoration.Underline, style = MaterialTheme.typography.bodyLarge)
                    else -> Text(m.body, style = MaterialTheme.typography.bodyLarge)
                }
                Row(Modifier.align(Alignment.End), verticalAlignment = Alignment.CenterVertically) {
                    Text(clock.format(Date(m.at)), style = MaterialTheme.typography.labelSmall, color = ink.copy(alpha = 0.7f))
                    if (m.out) {
                        Spacer(Modifier.width(4.dp))
                        Icon(
                            if (m.waiting) Icons.Filled.Schedule else Icons.Filled.Done,
                            if (m.waiting) "waiting" else "delivered",
                            tint = ink.copy(alpha = 0.7f),
                            modifier = Modifier.size(14.dp),
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun Composer(draft: String, onDraft: (String) -> Unit, canAttach: Boolean, onAttach: () -> Unit, onSend: () -> Unit) {
    Surface(tonalElevation = 3.dp, modifier = Modifier.fillMaxWidth()) {
        Row(
            verticalAlignment = Alignment.Bottom,
            modifier = Modifier.navigationBarsPadding().imePadding().padding(horizontal = 8.dp, vertical = 8.dp),
        ) {
            if (canAttach) {
                IconButton(onClick = onAttach) { Icon(Icons.Filled.AttachFile, "Send a file") }
            }
            TextField(
                value = draft,
                onValueChange = onDraft,
                placeholder = { Text("Message") },
                maxLines = 5,
                shape = RoundedCornerShape(24.dp),
                colors = TextFieldDefaults.colors(
                    focusedIndicatorColor = Color.Transparent,
                    unfocusedIndicatorColor = Color.Transparent,
                ),
                modifier = Modifier.weight(1f),
            )
            Spacer(Modifier.width(8.dp))
            FilledIconButton(onClick = onSend, enabled = draft.isNotBlank(), modifier = Modifier.size(52.dp)) {
                Icon(Icons.AutoMirrored.Filled.Send, "Send")
            }
        }
    }
}

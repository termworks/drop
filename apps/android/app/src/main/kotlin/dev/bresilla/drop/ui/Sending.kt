package dev.bresilla.drop.ui

import android.net.Uri
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import dev.bresilla.drop.Drop
import dev.bresilla.drop.Machine
import kotlinx.coroutines.launch

/** The first web address in something shared, which is what opening it over there opens. */
private val address = Regex("""https?://\S+""")

/** Where a shared thing goes on a machine. */
private enum class Way { Files, Chat, Open }

/**
 * Another app handed something over: files, text, a link, or files with a caption. Files go to the
 * machine's inbox and show in the conversation; text is said in the conversation; a link can be
 * said, or opened over there.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SendingScreen(uris: List<Uri>, text: String?, back: () -> Unit, done: (String) -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var mine by remember { mutableStateOf<List<Machine>>(emptyList()) }
    var theirs by remember { mutableStateOf<List<Machine>>(emptyList()) }
    var sending by remember { mutableStateOf<String?>(null) }
    var failed by remember { mutableStateOf<String?>(null) }
    val link = text?.let { address.find(it)?.value }

    LaunchedEffect(Unit) {
        val people = Drop.people().getOrNull() ?: emptyList()
        mine = people.filter { it.me }.flatMap { it.machines }
        theirs = people.filter { !it.me }.flatMap { it.machines }
    }

    val send: (Machine, Way) -> Unit = { m, way ->
        sending = m.name
        failed = null
        scope.launch {
            failed = deliver(context, m.name, way, uris, text, link)
            sending = null
            if (failed == null) done(m.name)
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Text(
                        when {
                            uris.size > 1 -> "Send ${uris.size} files to…"
                            uris.size == 1 -> "Send a file to…"
                            link != null -> "Send a link to…"
                            else -> "Send to…"
                        },
                    )
                },
                navigationIcon = { IconButton(onClick = back) { Icon(Icons.Filled.Close, "Cancel") } },
            )
        },
    ) { pad ->
        LazyColumn(contentPadding = PaddingValues(top = pad.calculateTopPadding(), bottom = 32.dp)) {
            text?.let { item { Banner(it.take(400)) } }
            failed?.let { item { Banner(it, error = true) } }
            item { Transfer() }
            if (mine.isEmpty() && theirs.isEmpty()) item { Banner("Pair with somebody first, and they show up here.") }

            for ((heading, machines) in listOf("Your machines" to mine, "People" to theirs)) {
                if (machines.isEmpty()) continue
                item { Section(heading) }
                items(machines, key = { heading + it.id }) { m ->
                    val first = if (uris.isNotEmpty()) Way.Files else Way.Chat
                    ListItem(
                        modifier = Modifier
                            .padding(horizontal = 12.dp, vertical = 3.dp)
                            .clip(RoundedCornerShape(20.dp))
                            .clickable(enabled = sending == null) { send(m, first) },
                        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
                        leadingContent = { Avatar(m.name, online = m.reaching) },
                        headlineContent = { Text(m.name, style = MaterialTheme.typography.titleMedium) },
                        supportingContent = { Text(if (m.reaching) "online" else m.brief, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                        trailingContent = {
                            when {
                                sending == m.name -> CircularProgressIndicator(strokeWidth = 2.dp)
                                // A link alone can be said in the conversation or opened over there.
                                link != null && uris.isEmpty() -> Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                                    OutlinedButton(onClick = { send(m, Way.Chat) }, enabled = sending == null) { Text("Chat") }
                                    FilledTonalButton(onClick = { send(m, Way.Open) }, enabled = sending == null) { Text("Open there") }
                                }
                            }
                        },
                    )
                }
            }
        }
    }
}

/** Sends what was shared to a machine, one way, and says why not when it could not. */
private suspend fun deliver(
    context: android.content.Context,
    machine: String,
    way: Way,
    uris: List<Uri>,
    text: String?,
    link: String?,
): String? {
    val paths = Drop.paths(machine).getOrNull()?.paths ?: emptyList()
    when (way) {
        Way.Open -> {
            val opens = paths.firstOrNull { it.kind == "link" && !it.locked } ?: return "$machine is not taking links from you"
            Drop.call { it.post(machine, opens.path, opens.archetype, link ?: return@call) }.onFailure { return it.message }
            return null
        }
        Way.Files -> {
            // An inbox first, which hands the files over and says so in the conversation; else a
            // folder that takes things put in it.
            val inbox = paths.firstOrNull { it.kind == "share" && !it.locked }
            val folder = paths.firstOrNull { it.kind == "files" && it.writable && !it.locked }
            if (inbox == null && folder == null) return "$machine is not taking files from you"
            val staged = stage(context, uris)
            if (staged.size < uris.size) {
                staged.forEach { it.delete() }
                return "Could not read what was shared: the app that shared it did not let this one open it"
            }
            val sent = if (inbox != null) {
                Drop.call { it.send(machine, inbox.path, staged.joinToString("\n") { f -> f.absolutePath }) }
            } else {
                Drop.call { node -> staged.forEach { f -> node.put(machine, folder!!.path, "", f.absolutePath) } }
            }
            staged.forEach { it.delete() }
            sent.onFailure { return it.message }
            // A caption goes with the files, said in the conversation they arrive in.
            if (!text.isNullOrBlank()) Drop.call { it.say(machine, text) }.onFailure { return it.message }
            return null
        }
        Way.Chat -> {
            val said = text?.takeIf { it.isNotBlank() } ?: return "There is nothing to say"
            Drop.call { it.say(machine, said) }.onFailure { return it.message }
            Drop.call { it.deliver(machine) }
            return null
        }
    }
}

package dev.bresilla.drop.ui

import android.net.Uri
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
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
import androidx.compose.ui.unit.dp
import dev.bresilla.drop.Drop
import dev.bresilla.drop.Machine
import kotlinx.coroutines.launch

/** Another app handed something over: whom does it go to? */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SendingScreen(uris: List<Uri>, back: () -> Unit, done: (String) -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var machines by remember { mutableStateOf<List<Machine>>(emptyList()) }
    var sending by remember { mutableStateOf<String?>(null) }
    var failed by remember { mutableStateOf<String?>(null) }

    LaunchedEffect(Unit) { machines = Drop.people().getOrNull()?.flatMap { it.machines } ?: emptyList() }

    val send: (Machine) -> Unit = { m ->
        sending = m.name
        failed = null
        scope.launch {
            val inbox = Drop.paths(m.name).getOrNull()?.paths?.firstOrNull { it.kind == "share" && !it.locked }
            if (inbox == null) {
                failed = "${m.name} is not taking files from you right now"
            } else {
                val staged = stage(context, uris)
                Drop.call { it.send(m.name, inbox.path, staged.joinToString("\n") { f -> f.absolutePath }) }
                    .onSuccess { done(m.name) }
                    .onFailure { failed = it.message }
                staged.forEach { it.delete() }
            }
            sending = null
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(if (uris.size == 1) "Send one file to…" else "Send ${uris.size} files to…") },
                navigationIcon = { IconButton(onClick = back) { Icon(Icons.Filled.Close, "Cancel") } },
            )
        },
    ) { pad ->
        LazyColumn(contentPadding = PaddingValues(top = pad.calculateTopPadding(), bottom = 32.dp)) {
            failed?.let { item { Banner(it, error = true) } }
            item { Transfer() }
            if (machines.isEmpty()) item { Banner("Pair with somebody first, and they show up here.") }
            items(machines, key = { it.id }) { m ->
                ListItem(
                    modifier = Modifier
                        .padding(horizontal = 12.dp, vertical = 3.dp)
                        .clip(RoundedCornerShape(20.dp))
                        .clickable(enabled = sending == null) { send(m) },
                    colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
                    leadingContent = { Avatar(m.name, online = m.reaching) },
                    headlineContent = { Text(m.name, style = MaterialTheme.typography.titleMedium) },
                    supportingContent = { Text(if (m.reaching) "online" else m.brief) },
                    trailingContent = { if (sending == m.name) CircularProgressIndicator(strokeWidth = 2.dp) },
                )
            }
        }
    }
}

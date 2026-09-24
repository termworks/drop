package dev.bresilla.drop.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Block
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.RadioButton
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
import androidx.compose.ui.unit.dp
import dev.bresilla.drop.Drop
import dev.bresilla.drop.PathDetail
import dev.bresilla.drop.WhoState
import kotlinx.coroutines.launch

/**
 * Who may reach one path: on this phone when machine is empty, or on another machine of yours, which
 * takes the change from here because this phone carries your badge.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AccessScreen(machine: String, path: String, back: () -> Unit) {
    val scope = rememberCoroutineScope()
    val said = remember { SnackbarHostState() }
    val tick by Drop.tick.collectAsState()
    var detail by remember { mutableStateOf<PathDetail?>(null) }
    var failed by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    var adding by remember { mutableStateOf(false) }

    LaunchedEffect(tick) {
        Drop.manage(machine, "read", path)
            .onSuccess { detail = it; failed = null }
            .onFailure { if (detail == null) failed = it.message }
    }

    val change: (String, String, String, Boolean) -> Unit = { op, who, level, shown ->
        busy = true
        scope.launch {
            Drop.manage(machine, op, path, who, level, shown)
                .onSuccess { detail = it }
                .onFailure { said.showSnackbar(it.message ?: "Could not change it") }
            busy = false
        }
    }
    val on = machine.ifEmpty { "this phone" }

    Scaffold(
        snackbarHost = { SnackbarHost(said) },
        topBar = {
            TopAppBar(
                title = {
                    Column {
                        Text(path.trimStart('/').ifEmpty { "/" })
                        Text("who can open it, on $on", style = Mono, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                },
                navigationIcon = { IconButton(onClick = back) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
                actions = { if (busy) CircularProgressIndicator(Modifier.padding(12.dp), strokeWidth = 2.dp) },
            )
        },
    ) { pad ->
        val d = detail
        LazyColumn(contentPadding = PaddingValues(top = pad.calculateTopPadding(), bottom = 32.dp)) {
            failed?.let { item { Banner("Could not ask $on: $it", error = true) } }
            if (d == null) {
                if (failed == null) item { Box(Modifier.fillMaxWidth().padding(32.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator() } }
                return@LazyColumn
            }
            val s = d.state

            item { Section("Who can open it") }
            items(Steps, key = { it.level }) { step ->
                ListItem(
                    modifier = Modifier
                        .padding(horizontal = 12.dp, vertical = 2.dp)
                        .clip(RoundedCornerShape(18.dp))
                        .clickable(enabled = !busy && s.level != step.level) { change("level", "", step.level, false) },
                    colors = ListItemDefaults.colors(
                        containerColor = if (s.level == step.level) step.tint.copy(alpha = 0.12f) else MaterialTheme.colorScheme.surfaceContainerLow,
                    ),
                    leadingContent = { Icon(step.icon, null, tint = step.tint) },
                    headlineContent = { Text(step.title, style = MaterialTheme.typography.titleMedium) },
                    supportingContent = { Text(step.says, color = MaterialTheme.colorScheme.onSurfaceVariant) },
                    trailingContent = { RadioButton(selected = s.level == step.level, onClick = null) },
                )
            }
            if (s.level == "custom") {
                item { Banner("Its config decides who, in a way none of these steps says. Choosing a step replaces that.") }
            }
            if (s.password) {
                item { Banner("A password guards it as well: whoever knows it gets in.") }
            }
            if (s.chosen) {
                item {
                    TextButton(onClick = { change("level", "", "", false) }, enabled = !busy, modifier = Modifier.padding(horizontal = 12.dp)) {
                        Text("Back to what its config says: ${stepOf(s.config).title}")
                    }
                }
            }

            if (s.level == "me" || s.level == "trusted" || s.level == "custom") {
                item { Section("Seen by others") }
                item {
                    Toggle(
                        title = "Show it to people who can't open it",
                        says = "Everybody paired sees it is there, and can ask to be let in.",
                        on = s.shown,
                        enabled = !busy,
                    ) { change("shown", "", "", it) }
                }
            }

            if (d.asking.isNotEmpty()) {
                item { Section("Asking to be let in") }
                items(d.asking, key = { "a:" + it.who }) { a ->
                    ListItem(
                        modifier = Modifier.padding(horizontal = 12.dp, vertical = 3.dp).clip(RoundedCornerShape(18.dp)),
                        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.tertiaryContainer),
                        leadingContent = { Avatar(a.who) },
                        headlineContent = { Text(a.who, style = MaterialTheme.typography.titleMedium) },
                        supportingContent = {
                            Column {
                                Text(listOfNotNull(a.why.ifEmpty { null }?.let { "“$it”" }, a.`when`).joinToString(" · "))
                                Row(horizontalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.padding(top = 6.dp)) {
                                    FilledTonalButton(onClick = { change("allow", a.who, "", false) }, enabled = !busy) { Text("Let in") }
                                    OutlinedButton(onClick = { change("deny", a.who, "", false) }, enabled = !busy) { Text("Keep out") }
                                }
                            }
                        },
                    )
                }
            }

            val exceptions = d.who.filter { it.at.isNotEmpty() }
            item { Section("Exceptions") }
            if (exceptions.isEmpty()) {
                item { Banner("Nobody is let in or kept out by name: the step above decides for everybody.") }
            }
            items(exceptions, key = { "e:" + it.name }) { w -> Excepted(w, busy) { change("unset", w.name, "", false) } }
            item {
                TextButton(onClick = { adding = true }, enabled = !busy && d.who.isNotEmpty(), modifier = Modifier.padding(horizontal = 12.dp)) {
                    Icon(Icons.Filled.Add, null)
                    Text("Let somebody in, or keep somebody out")
                }
            }
        }
    }

    if (adding) {
        val d = detail
        var chosen by remember { mutableStateOf<WhoState?>(null) }
        AlertDialog(
            onDismissRequest = { adding = false },
            title = { Text(chosen?.name ?: "Who?") },
            text = {
                val who = chosen
                if (who == null) {
                    LazyColumn(Modifier.heightIn(max = 360.dp)) {
                        items(d?.who ?: emptyList(), key = { it.name }) { w ->
                            ListItem(
                                modifier = Modifier.clip(RoundedCornerShape(14.dp)).clickable { chosen = w },
                                leadingContent = { Avatar(w.name, size = 36.dp) },
                                headlineContent = { Text(w.name) },
                                supportingContent = { Text(if (w.person) "a person" else "a machine on its own") },
                            )
                        }
                    }
                } else {
                    Text("Let ${who.name} in whatever the step says, or keep them out whatever it says.")
                }
            },
            confirmButton = {
                val who = chosen
                if (who != null) {
                    Row {
                        TextButton(onClick = { adding = false; change("deny", who.name, "", false) }) { Text("Keep out") }
                        TextButton(onClick = { adding = false; change("allow", who.name, "", false) }) { Text("Let in") }
                    }
                }
            },
            dismissButton = { TextButton(onClick = { adding = false }) { Text("Cancel") } },
        )
    }
}

@Composable
private fun Excepted(w: WhoState, busy: Boolean, onRemove: () -> Unit) {
    val allowed = w.at == "allowed"
    ListItem(
        modifier = Modifier.padding(horizontal = 12.dp, vertical = 3.dp).clip(RoundedCornerShape(18.dp)),
        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
        leadingContent = { Avatar(w.name) },
        headlineContent = { Text(w.name, style = MaterialTheme.typography.titleMedium) },
        supportingContent = {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                Icon(
                    if (allowed) Icons.Filled.CheckCircle else Icons.Filled.Block, null,
                    tint = if (allowed) MaterialTheme.colorScheme.tertiary else MaterialTheme.colorScheme.error,
                )
                Text(
                    (if (allowed) "let in" else "kept out") + if (w.inConfig) " · named in its config" else "",
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        },
        trailingContent = {
            if (!(allowed && w.inConfig)) TextButton(onClick = onRemove, enabled = !busy) { Text("Remove") }
        },
    )
}

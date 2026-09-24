package dev.bresilla.drop.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Block
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.RadioButtonUnchecked
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
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
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import dev.bresilla.drop.Drop
import dev.bresilla.drop.Managed
import dev.bresilla.drop.Rule
import dev.bresilla.drop.Standing
import kotlinx.coroutines.launch

/** Who may reach one of this phone's paths, and where that is changed. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AccessScreen(path: String, back: () -> Unit) {
    val scope = rememberCoroutineScope()
    val tick by Drop.tick.collectAsState()
    var rule by remember { mutableStateOf<Rule?>(null) }
    var choosing by remember { mutableStateOf<Standing?>(null) }

    LaunchedEffect(tick) { rule = Drop.access(path).getOrNull() ?: rule }

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Column {
                        Text(path.trimStart('/').ifEmpty { "/" })
                        Text("who may reach it", style = Mono, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                },
                navigationIcon = { IconButton(onClick = back) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
            )
        },
    ) { pad ->
        val r = rule
        LazyColumn(contentPadding = PaddingValues(top = pad.calculateTopPadding(), bottom = 32.dp)) {
            if (r != null) {
                item {
                    Banner(
                        when {
                            r.anyone -> "Anybody who knows this phone's id may reach it, paired or not."
                            r.paired -> "Everybody paired with this phone may reach it, except anybody kept out below."
                            else -> "Only the people let in below may reach it."
                        } + if (r.password) " A password guards it as well." else "",
                    )
                }
                if (r.asked.isNotEmpty()) {
                    item { Section("Asking to be let in") }
                    items(r.asked, key = { "a:" + it.who }) { a ->
                        ListItem(
                            modifier = Modifier.padding(horizontal = 12.dp, vertical = 3.dp).clip(RoundedCornerShape(20.dp)),
                            colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.tertiaryContainer),
                            leadingContent = { Avatar(a.who) },
                            headlineContent = { Text(a.who) },
                            supportingContent = { if (a.why.isNotEmpty()) Text("“${a.why}”") },
                            trailingContent = {
                                Row {
                                    TextButton(onClick = { scope.launch { Drop.call { it.refuse(path, a.who) } } }) { Text("Keep out") }
                                    OutlinedButton(onClick = { scope.launch { Drop.call { it.grant(path, a.who) } } }) { Text("Let in") }
                                }
                            },
                        )
                    }
                }
                item { Section("People and machines") }
                items(r.who, key = { "w:" + it.name }) { w ->
                    ListItem(
                        modifier = Modifier
                            .padding(horizontal = 12.dp, vertical = 3.dp)
                            .clip(RoundedCornerShape(20.dp))
                            .clickable { choosing = w },
                        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
                        leadingContent = { Avatar(w.name) },
                        headlineContent = { Text(w.name, style = MaterialTheme.typography.titleMedium) },
                        supportingContent = {
                            Text(
                                listOfNotNull(
                                    if (w.person) "${w.machines} machine" + if (w.machines == 1) "" else "s" else "one machine",
                                    if (w.inConfig) "named in the config" else null,
                                ).joinToString(" · "),
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        },
                        trailingContent = { StandingMark(w.at, r.paired || r.anyone) },
                    )
                }
            }
        }
    }

    choosing?.let { w ->
        AlertDialog(
            onDismissRequest = { choosing = null },
            title = { Text("${w.name} and ${path.trimStart('/')}") },
            text = {
                Text(
                    "Let in names them on this path. Keep out shuts them out whatever else says. " +
                        "Leave to the rule takes this phone's word back, and the path's own rule decides.",
                )
            },
            confirmButton = {
                Row {
                    TextButton(onClick = { choosing = null; scope.launch { Drop.call { it.refuse(path, w.name) } } }) { Text("Keep out") }
                    TextButton(onClick = { choosing = null; scope.launch { Drop.call { it.grant(path, w.name) } } }) { Text("Let in") }
                }
            },
            dismissButton = {
                TextButton(onClick = { choosing = null; scope.launch { Drop.call { it.unset(path, w.name) } } }) { Text("Leave to the rule") }
            },
        )
    }
}

/** How somebody stands, as a mark: let in, kept out, or left to the rule — which may itself let them in. */
@Composable
private fun StandingMark(at: String, ruleLetsIn: Boolean) {
    when {
        at == "allowed" -> Icon(Icons.Filled.CheckCircle, "let in", tint = MaterialTheme.colorScheme.tertiary)
        at == "refused" -> Icon(Icons.Filled.Block, "kept out", tint = MaterialTheme.colorScheme.error)
        ruleLetsIn -> Icon(Icons.Filled.CheckCircle, "let in by the rule", tint = MaterialTheme.colorScheme.outline)
        else -> Icon(Icons.Filled.RadioButtonUnchecked, "not let in", tint = MaterialTheme.colorScheme.outline)
    }
}

/** Somebody: who they are, whether they are trusted, and what this phone has let them into. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ManageScreen(name: String, back: () -> Unit, home: () -> Unit) {
    val scope = rememberCoroutineScope()
    val tick by Drop.tick.collectAsState()
    var who by remember { mutableStateOf<Managed?>(null) }
    var forgetting by remember { mutableStateOf(false) }

    LaunchedEffect(tick) { who = Drop.managed(name).getOrNull() ?: who }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(name) },
                navigationIcon = { IconButton(onClick = back) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
            )
        },
    ) { pad ->
        val m = who
        LazyColumn(contentPadding = PaddingValues(top = pad.calculateTopPadding(), bottom = 32.dp)) {
            if (m != null) {
                item {
                    Row(Modifier.padding(20.dp)) {
                        Avatar(m.name, size = 56.dp, online = m.reaching)
                        Spacer(Modifier.width(16.dp))
                        Column {
                            Text(m.name, style = MaterialTheme.typography.headlineSmall)
                            Text(
                                listOfNotNull(
                                    m.person.ifEmpty { null }?.let { "belongs to $it" },
                                    if (m.machines > 1) "${m.machines} machines" else null,
                                    if (m.reaching) "connected now" else null,
                                ).joinToString(" · "),
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            if (m.id.isNotEmpty()) Text(m.id, style = Mono, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                    }
                }
                item { Section("Trust") }
                item {
                    Toggle(
                        title = "Trusted",
                        says = "Pairing is recognising somebody. Trust is the second, deliberate step, and what paths that say \"trusted\" let in.",
                        on = m.trusted,
                    ) { on -> scope.launch { Drop.call { it.trust(name, on) }; Drop.bump() } }
                }
                item { Section("On this phone") }
                if (m.allowed.isEmpty() && m.refused.isEmpty()) {
                    item { Banner("Nothing decided about them here: every path's own rule decides.") }
                }
                items(m.allowed, key = { "y:$it" }) { path ->
                    Decided(path, "let in", Color(0xFF2DB55D)) { scope.launch { Drop.call { it.unset(path, name) } } }
                }
                items(m.refused, key = { "n:$it" }) { path ->
                    Decided(path, "kept out", MaterialTheme.colorScheme.error) { scope.launch { Drop.call { it.unset(path, name) } } }
                }
                item { Section("Pairing") }
                item {
                    ListItem(
                        modifier = Modifier
                            .padding(horizontal = 12.dp, vertical = 3.dp)
                            .clip(RoundedCornerShape(20.dp))
                            .clickable { forgetting = true },
                        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.errorContainer),
                        headlineContent = { Text("Forget $name", color = MaterialTheme.colorScheme.onErrorContainer) },
                        supportingContent = {
                            Text("They arrive as a stranger from then on, and have to pair again.", color = MaterialTheme.colorScheme.onErrorContainer)
                        },
                    )
                }
            }
        }
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

@Composable
private fun Decided(path: String, how: String, tint: Color, onUndo: () -> Unit) {
    ListItem(
        modifier = Modifier.padding(horizontal = 12.dp, vertical = 3.dp).clip(RoundedCornerShape(20.dp)),
        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
        headlineContent = { Text(path, style = MaterialTheme.typography.titleMedium) },
        supportingContent = { Text(how, color = tint) },
        trailingContent = { TextButton(onClick = onUndo) { Text("Undo") } },
    )
}

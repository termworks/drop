package dev.bresilla.drop.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
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
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.PersonAdd
import androidx.compose.material.icons.filled.QrCode2
import androidx.compose.material.icons.filled.QrCodeScanner
import androidx.compose.material.icons.filled.Star
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import dev.bresilla.drop.Drop
import dev.bresilla.drop.Person
import kotlinx.coroutines.launch

/** Everybody you paired with, one row a person however many machines they have. Yours are not here. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PeopleTab(go: (Screen) -> Unit) {
    val tick by Drop.tick.collectAsState()
    val trouble by Drop.trouble.collectAsState()
    var people by remember { mutableStateOf<List<Person>?>(null) }
    var failed by remember { mutableStateOf<String?>(null) }

    LaunchedEffect(tick) {
        Drop.people()
            .onSuccess { people = it.filter { p -> !p.me }; failed = null }
            .onFailure { failed = it.message }
    }

    Scaffold(
        contentWindowInsets = WindowInsets(0),
        topBar = { TopAppBar(title = { Text("People") }) },
        floatingActionButton = {
            ExtendedFloatingActionButton(
                onClick = { go(Screen.Pair()) },
                icon = { Icon(Icons.Filled.PersonAdd, null) },
                text = { Text("Add person") },
            )
        },
    ) { pad ->
        LazyColumn(contentPadding = PaddingValues(top = pad.calculateTopPadding(), bottom = 96.dp)) {
            trouble?.let { text -> item { Box(Modifier.clickable { Drop.clearTrouble() }) { Banner(text, error = true) } } }
            failed?.let { text -> item { Banner(text, error = true) } }

            val all = people
            when {
                all == null -> item { Box(Modifier.fillMaxWidth().padding(48.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator() } }
                all.isEmpty() -> item { Nobody(go) }
                else -> items(all.flatMap { p -> if (p.anon) p.machines.map { m -> Person(m.name, false, true, m.trusted, listOf(m)) } else listOf(p) }, key = { it.name }) { p ->
                    val one = p.machines.singleOrNull()
                    Entry(
                        title = p.name,
                        subtitle = when {
                            p.anon -> "a machine on its own" + if (p.reaching > 0) " · online" else ""
                            one != null -> one.name + if (one.reaching) " · online" else ""
                            else -> "${p.machines.size} machines" + if (p.reaching > 0) " · ${p.reaching} online" else ""
                        },
                        online = p.reaching > 0,
                        trusted = p.trusted,
                    ) { go(if (p.anon && one != null) Screen.Machine(one.name) else Screen.Person(p.name)) }
                }
            }
        }
    }
}

@Composable
private fun Nobody(go: (Screen) -> Unit) {
    Card(
        modifier = Modifier.padding(16.dp).fillMaxWidth(),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
        shape = RoundedCornerShape(24.dp),
    ) {
        Column(Modifier.padding(24.dp).fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally) {
            Text("Nobody here yet", style = MaterialTheme.typography.titleLarge)
            Spacer(Modifier.height(8.dp))
            Text(
                "Pair once with a friend's phone or computer, and you can reach each other from anywhere after that.",
                style = MaterialTheme.typography.bodyMedium,
                textAlign = TextAlign.Center,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(16.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                Button(onClick = { go(Screen.Pair()) }) {
                    Icon(Icons.Filled.QrCode2, null, Modifier.size(18.dp))
                    Spacer(Modifier.width(6.dp))
                    Text("Show my code")
                }
                FilledTonalButton(onClick = { go(Screen.Pair(scan = true)) }) {
                    Icon(Icons.Filled.QrCodeScanner, null, Modifier.size(18.dp))
                    Spacer(Modifier.width(6.dp))
                    Text("Scan theirs")
                }
            }
        }
    }
}

@Composable
fun Entry(title: String, subtitle: String, online: Boolean, trusted: Boolean, onClick: () -> Unit) {
    ListItem(
        modifier = Modifier
            .padding(horizontal = 12.dp, vertical = 3.dp)
            .clip(RoundedCornerShape(20.dp))
            .clickable(onClick = onClick),
        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
        leadingContent = { Avatar(title, online = online) },
        headlineContent = {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(title, style = MaterialTheme.typography.titleMedium)
                if (trusted) {
                    Spacer(Modifier.width(6.dp))
                    Icon(Icons.Filled.Star, "trusted", tint = Color(0xFFE0A21F), modifier = Modifier.size(16.dp))
                }
            }
        },
        supportingContent = { Text(subtitle, color = MaterialTheme.colorScheme.onSurfaceVariant) },
        trailingContent = { Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, null) },
    )
}

/** Somebody: their machines, whether you trust them, and the name you file them under. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PersonScreen(name: String, go: (Screen) -> Unit, back: () -> Unit, home: () -> Unit) {
    val scope = rememberCoroutineScope()
    val tick by Drop.tick.collectAsState()
    val said = remember { SnackbarHostState() }
    var person by remember { mutableStateOf<Person?>(null) }
    var menu by remember { mutableStateOf(false) }
    var renaming by remember { mutableStateOf(false) }
    var removing by remember { mutableStateOf(false) }

    Pulse()
    LaunchedEffect(tick) { person = Drop.people().getOrNull()?.firstOrNull { it.name == name } ?: person }

    Scaffold(
        snackbarHost = { SnackbarHost(said) },
        topBar = {
            TopAppBar(
                title = { Text(name) },
                navigationIcon = { IconButton(onClick = back) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
                actions = {
                    IconButton(onClick = { menu = true }) { Icon(Icons.Filled.MoreVert, "More") }
                    DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                        DropdownMenuItem(text = { Text("Rename") }, onClick = { menu = false; renaming = true })
                        DropdownMenuItem(text = { Text("Remove $name") }, onClick = { menu = false; removing = true })
                    }
                },
            )
        },
    ) { pad ->
        val p = person
        LazyColumn(contentPadding = PaddingValues(top = pad.calculateTopPadding(), bottom = 32.dp)) {
            item {
                Row(Modifier.padding(20.dp), verticalAlignment = Alignment.CenterVertically) {
                    Avatar(name, size = 64.dp, online = p?.let { it.reaching > 0 })
                    Spacer(Modifier.width(16.dp))
                    Column {
                        Text(name, style = MaterialTheme.typography.headlineSmall)
                        Text(
                            p?.let { "${it.machines.size} machine" + (if (it.machines.size == 1) "" else "s") + if (it.reaching > 0) " · ${it.reaching} online" else "" } ?: "",
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }
            if (p != null) {
                item {
                    Toggle(
                        title = "Trusted",
                        says = "Paths set to Trusted open for them. Pairing only recognises somebody; trusting them is the second step.",
                        on = p.trusted,
                    ) { on -> scope.launch { Drop.call { it.trust(name, on) }; Drop.bump() } }
                }
                item { Section("Their machines") }
                items(p.machines, key = { it.id }) { m ->
                    Entry(m.name, m.brief + if (m.reaching) " · online" else "", m.reaching, false) { go(Screen.Machine(m.name)) }
                }
            }
        }
    }

    if (renaming) {
        Renaming(name, done = { renaming = false }) { called ->
            scope.launch {
                Drop.call { it.rename(name, called) }
                    .onSuccess { home() }
                    .onFailure { said.showSnackbar(it.message ?: "Could not rename") }
            }
        }
    }

    if (removing) {
        AlertDialog(
            onDismissRequest = { removing = false },
            title = { Text("Remove $name?") },
            text = { Text("Every machine of theirs is forgotten here, and arrives as a stranger from then on. You would have to pair again.") },
            confirmButton = {
                TextButton(onClick = {
                    removing = false
                    scope.launch {
                        Drop.call { it.forget(name) }
                        home()
                    }
                }) { Text("Remove") }
            },
            dismissButton = { TextButton(onClick = { removing = false }) { Text("Cancel") } },
        )
    }
}

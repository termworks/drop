package dev.bresilla.drop.ui

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
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.PersonAdd
import androidx.compose.material.icons.filled.QrCode2
import androidx.compose.material.icons.filled.QrCodeScanner
import androidx.compose.material.icons.filled.Star
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Badge
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
import androidx.compose.material3.OutlinedButton
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import dev.bresilla.drop.Drop
import dev.bresilla.drop.Knock
import dev.bresilla.drop.Machine
import dev.bresilla.drop.Me
import dev.bresilla.drop.Person
import kotlinx.coroutines.launch

/** What "me" is called in the address book, the way the node arranges it. */
const val ME = "me"

/**
 * Where the app opens: you first, with every machine of yours, then everybody you paired with.
 * What arrived and is not read yet shows on whoever it came from.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PeopleScreen(go: (Screen) -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val tick by Drop.tick.collectAsState()
    val trouble by Drop.trouble.collectAsState()
    var me by remember { mutableStateOf<Me?>(null) }
    var people by remember { mutableStateOf<List<Person>?>(null) }
    var failed by remember { mutableStateOf<String?>(null) }
    var unread by remember { mutableStateOf<Map<String, Int>>(emptyMap()) }
    var asking by remember { mutableStateOf(0) }
    var knocks by remember { mutableStateOf<List<Knock>>(emptyList()) }
    var menu by remember { mutableStateOf(false) }

    Pulse()
    LaunchedEffect(tick) {
        me = Drop.self() ?: me
        Drop.people()
            .onSuccess { people = it; failed = null }
            .onFailure { failed = it.message }
        unread = unreadBy(context)
        asking = Drop.levels("").getOrNull()?.sumOf { it.asked } ?: asking
        knocks = Drop.knocked().getOrNull() ?: knocks
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("People") },
                actions = {
                    IconButton(onClick = { go(Screen.Pair(scan = true)) }) { Icon(Icons.Filled.QrCodeScanner, "Scan a code") }
                    IconButton(onClick = { menu = true }) { Icon(Icons.Filled.MoreVert, "More") }
                    DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                        DropdownMenuItem(text = { Text("Show my code") }, onClick = { menu = false; go(Screen.Pair()) })
                        DropdownMenuItem(text = { Text("Add a machine of mine") }, onClick = { menu = false; go(Screen.AddMachine) })
                        DropdownMenuItem(text = { Text("Settings") }, onClick = { menu = false; go(Screen.Settings) })
                    }
                },
            )
        },
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
            if (all == null) {
                item { Box(Modifier.fillMaxWidth().padding(48.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator() } }
                return@LazyColumn
            }

            val mine = all.firstOrNull { it.me }?.machines ?: emptyList()
            item {
                Entry(
                    title = "Me",
                    subtitle = (listOf(me?.name ?: "this phone") + mine.map { it.name }).joinToString(" · ") +
                        if (asking > 0) " · $asking asking" else "",
                    online = true,
                    trusted = false,
                    unread = mine.sumOf { unread[it.name] ?: 0 },
                    avatar = me?.name ?: ME,
                    highlight = true,
                ) { go(Screen.Person(ME)) }
            }

            val others = all.filter { !it.me }.flatMap { p ->
                if (p.anon) p.machines.map { m -> Person(m.name, false, true, m.trusted, listOf(m)) } else listOf(p)
            }
            item { Section("People") }
            if (others.isEmpty()) item { Nobody(go) }
            items(others, key = { "p:" + it.name }) { p ->
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
                    unread = p.machines.sumOf { unread[it.name] ?: 0 },
                ) { go(if (p.anon && one != null) Screen.Machine(one.name) else Screen.Person(p.name)) }
            }

            if (knocks.isNotEmpty()) {
                item { Section("Strangers who tried this phone") }
                items(knocks, key = { "k:" + it.id }) { k ->
                    ListItem(
                        modifier = Modifier.padding(horizontal = 12.dp, vertical = 3.dp).clip(RoundedCornerShape(20.dp)),
                        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
                        headlineContent = { Text(k.brief, style = Mono) },
                        supportingContent = {
                            Text(
                                listOfNotNull(k.asked.ifEmpty { null }, k.why.ifEmpty { null }, ago(k.at)).joinToString(" · "),
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        },
                        trailingContent = {
                            if (k.asked.isNotEmpty()) {
                                OutlinedButton(onClick = { scope.launch { Drop.manage("", "allow", k.asked, k.id) } }) { Text("Let in") }
                            }
                        },
                    )
                }
            }
        }
    }
}

@Composable
private fun Nobody(go: (Screen) -> Unit) {
    Card(
        modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp).fillMaxWidth(),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
        shape = RoundedCornerShape(24.dp),
    ) {
        Column(Modifier.padding(24.dp).fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally) {
            Text("Nobody else yet", style = MaterialTheme.typography.titleLarge)
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
fun Entry(
    title: String,
    subtitle: String,
    online: Boolean,
    trusted: Boolean,
    unread: Int = 0,
    avatar: String = title,
    highlight: Boolean = false,
    onClick: () -> Unit,
) {
    ListItem(
        modifier = Modifier
            .padding(horizontal = 12.dp, vertical = 3.dp)
            .clip(RoundedCornerShape(20.dp))
            .clickable(onClick = onClick),
        colors = ListItemDefaults.colors(
            containerColor = if (highlight) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surfaceContainerLow,
        ),
        leadingContent = { Avatar(avatar, online = online) },
        headlineContent = {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(title, style = MaterialTheme.typography.titleMedium)
                if (trusted) {
                    Spacer(Modifier.width(6.dp))
                    Icon(Icons.Filled.Star, "trusted", tint = Color(0xFFE0A21F), modifier = Modifier.size(16.dp))
                }
            }
        },
        supportingContent = { Text(subtitle, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 2) },
        trailingContent = {
            if (unread > 0) Badge { Text(if (unread > 99) "99+" else "$unread") }
            else Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, null)
        },
    )
}

/**
 * Somebody and their machines. For you, that is this phone and every other machine of yours, and the
 * way to add one; for anybody else, whether you trust them and the name you file them under.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PersonScreen(name: String, go: (Screen) -> Unit, back: () -> Unit, home: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val tick by Drop.tick.collectAsState()
    val said = remember { SnackbarHostState() }
    val itsMe = name == ME
    var me by remember { mutableStateOf<Me?>(null) }
    var person by remember { mutableStateOf<Person?>(null) }
    var unread by remember { mutableStateOf<Map<String, Int>>(emptyMap()) }
    var asking by remember { mutableStateOf(0) }
    var menu by remember { mutableStateOf(false) }
    var renaming by remember { mutableStateOf(false) }
    var removing by remember { mutableStateOf(false) }

    Pulse()
    LaunchedEffect(tick) {
        val all = Drop.people().getOrNull()
        person = (if (itsMe) all?.firstOrNull { it.me } else all?.firstOrNull { it.name == name && !it.me }) ?: person
        unread = unreadBy(context)
        if (itsMe) {
            me = Drop.self() ?: me
            asking = Drop.levels("").getOrNull()?.sumOf { it.asked } ?: asking
        }
    }

    val machines: List<Machine> = person?.machines ?: emptyList()
    Scaffold(
        snackbarHost = { SnackbarHost(said) },
        topBar = {
            TopAppBar(
                title = { Text(if (itsMe) "Me" else name) },
                navigationIcon = { IconButton(onClick = back) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
                actions = {
                    IconButton(onClick = { menu = true }) { Icon(Icons.Filled.MoreVert, "More") }
                    DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                        if (itsMe) {
                            DropdownMenuItem(text = { Text("Add a machine") }, onClick = { menu = false; go(Screen.AddMachine) })
                            DropdownMenuItem(text = { Text("Settings") }, onClick = { menu = false; go(Screen.Settings) })
                        } else {
                            DropdownMenuItem(text = { Text("Rename") }, onClick = { menu = false; renaming = true })
                            DropdownMenuItem(text = { Text("Remove $name") }, onClick = { menu = false; removing = true })
                        }
                    }
                },
            )
        },
    ) { pad ->
        LazyColumn(contentPadding = PaddingValues(top = pad.calculateTopPadding(), bottom = 32.dp)) {
            item {
                Row(Modifier.padding(20.dp), verticalAlignment = Alignment.CenterVertically) {
                    Avatar(if (itsMe) me?.name ?: ME else name, size = 64.dp, online = if (itsMe) true else person?.let { it.reaching > 0 })
                    Spacer(Modifier.width(16.dp))
                    Column {
                        Text(if (itsMe) "Me" else name, style = MaterialTheme.typography.headlineSmall)
                        val count = machines.size + if (itsMe) 1 else 0
                        Text(
                            "$count machine" + (if (count == 1) "" else "s") +
                                if (itsMe) me?.owner?.let { " · $it" } ?: "" else if ((person?.reaching ?: 0) > 0) " · ${person?.reaching} online" else "",
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 1,
                        )
                    }
                }
            }

            if (!itsMe) {
                person?.let { p ->
                    item {
                        Toggle(
                            title = "Trusted",
                            says = "Paths set to Trusted open for them. Pairing only recognises somebody; trusting them is the second step.",
                            on = p.trusted,
                        ) { on -> scope.launch { Drop.call { it.trust(name, on) }; Drop.bump() } }
                    }
                }
            }

            item { Section(if (itsMe) "My machines" else "Their machines") }
            if (itsMe) {
                item {
                    Entry(
                        title = me?.name ?: "this phone",
                        subtitle = "this phone" + if (asking > 0) " · $asking asking to be let in" else "",
                        online = true,
                        trusted = false,
                    ) { go(Screen.Machine("")) }
                }
            }
            items(machines, key = { it.id }) { m ->
                Entry(
                    m.name,
                    m.brief + if (m.reaching) " · online" else "",
                    m.reaching,
                    false,
                    unread = unread[m.name] ?: 0,
                ) { go(Screen.Machine(m.name)) }
            }
            if (itsMe) {
                item {
                    TextButton(onClick = { go(Screen.AddMachine) }, modifier = Modifier.padding(horizontal = 12.dp)) {
                        Icon(Icons.Filled.Add, null)
                        Spacer(Modifier.width(6.dp))
                        Text("Add a machine")
                    }
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

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
import dev.bresilla.drop.PathOpen
import dev.bresilla.drop.Reachable
import dev.bresilla.drop.Me
import dev.bresilla.drop.Person
import dev.bresilla.drop.Settings
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
                title = { Text("drop", style = MaterialTheme.typography.headlineSmall) },
                actions = {
                    IconButton(onClick = { go(Screen.Pair(scan = true)) }) { Icon(Icons.Filled.QrCodeScanner, "Scan a code") }
                    IconButton(onClick = { menu = true }) { Icon(Icons.Filled.MoreVert, "More") }
                    DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                        DropdownMenuItem(text = { Text("Show my code") }, onClick = { menu = false; go(Screen.Pair()) })
                        DropdownMenuItem(text = { Text("Add a machine of mine") }, onClick = { menu = false; go(Screen.AddMachine()) })
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
                    title = Settings.me(context),
                    subtitle = (listOf(me?.name ?: "this phone") + mine.map { it.name }).joinToString(" · ") +
                        if (asking > 0) " · $asking asking" else "",
                    online = true,
                    trusted = false,
                    unread = mine.sumOf { unread[it.name] ?: 0 },
                    avatar = Settings.me(context),
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
            Column(verticalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.fillMaxWidth()) {
                Button(onClick = { go(Screen.Pair()) }, modifier = Modifier.fillMaxWidth()) {
                    Icon(Icons.Filled.QrCode2, null, Modifier.size(18.dp))
                    Spacer(Modifier.width(6.dp))
                    Text("Show my code")
                }
                FilledTonalButton(onClick = { go(Screen.Pair(scan = true)) }, modifier = Modifier.fillMaxWidth()) {
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
    actions: (@Composable () -> Unit)? = null,
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
            Row(verticalAlignment = Alignment.CenterVertically) {
                if (unread > 0) Badge { Text(if (unread > 99) "99+" else "$unread") }
                if (actions != null) actions()
                else if (unread == 0) Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, null)
            }
        },
    )
}

/**
 * Somebody and their machines. For you, that is this phone and every other machine of yours, each
 * renamed or taken out from its row, and the way to add one. For anybody else: whether you trust
 * them, what they may open on each machine of yours, and the name you file them under.
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
    var renaming by remember { mutableStateOf<String?>(null) }
    var removing by remember { mutableStateOf(false) }
    var takingOut by remember { mutableStateOf<String?>(null) }
    var reach by remember { mutableStateOf<List<Reachable>?>(null) }
    var reachFailed by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf<String?>(null) }
    var askAgain by remember { mutableStateOf(0) }

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
    // Asked once and after each change rather than on every tick: it is a question to every machine of yours.
    LaunchedEffect(name, askAgain) {
        if (itsMe) return@LaunchedEffect
        Drop.reachable(name)
            .onSuccess { reach = it; reachFailed = null }
            .onFailure { if (reach == null) reachFailed = it.message }
    }

    val machines: List<Machine> = person?.machines ?: emptyList()
    Scaffold(
        snackbarHost = { SnackbarHost(said) },
        topBar = {
            TopAppBar(
                title = { Text(if (itsMe) Settings.me(context) else name) },
                navigationIcon = { IconButton(onClick = back) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
                actions = {
                    IconButton(onClick = { menu = true }) { Icon(Icons.Filled.MoreVert, "More") }
                    DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                        if (itsMe) {
                            DropdownMenuItem(text = { Text("Add a machine") }, onClick = { menu = false; go(Screen.AddMachine()) })
                            DropdownMenuItem(text = { Text("Settings") }, onClick = { menu = false; go(Screen.Settings) })
                        } else {
                            DropdownMenuItem(text = { Text("Rename") }, onClick = { menu = false; renaming = name })
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
                    Avatar(if (itsMe) Settings.me(context) else name, size = 64.dp, online = if (itsMe) true else person?.let { it.reaching > 0 })
                    Spacer(Modifier.width(16.dp))
                    Column {
                        Text(if (itsMe) Settings.me(context) else name, style = MaterialTheme.typography.headlineSmall)
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

            if (itsMe) {
                item {
                    Row(Modifier.padding(horizontal = 20.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        FilledTonalButton(onClick = { go(Screen.AddMachine()) }) {
                            Icon(Icons.Filled.Add, null, Modifier.size(18.dp))
                            Spacer(Modifier.width(6.dp))
                            Text("Add a machine")
                        }
                    }
                }
            } else {
                item {
                    Row(Modifier.padding(horizontal = 20.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        FilledTonalButton(onClick = { renaming = name }) { Text("Rename") }
                        OutlinedButton(onClick = { removing = true }) { Text("Remove", color = MaterialTheme.colorScheme.error) }
                    }
                }
                person?.let { p ->
                    item {
                        Toggle(
                            title = "Trusted",
                            says = "Paths set to Trusted open for them. Pairing only recognises somebody; trusting them is the second step.",
                            on = p.trusted,
                        ) { on -> scope.launch { Drop.call { it.trust(name, on) }; Drop.bump(); askAgain++ } }
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
                    actions = if (itsMe) {
                        {
                            MachineActions(
                                rename = { renaming = m.name },
                                remove = { takingOut = m.name },
                            )
                        }
                    } else null,
                ) { go(Screen.Machine(m.name)) }
            }

            if (!itsMe) {
                item { Section("What they can open") }
                reachFailed?.let { item { Banner("Could not ask your machines: $it", error = true) } }
                if (reach == null && reachFailed == null) {
                    item { Box(Modifier.fillMaxWidth().padding(24.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator() } }
                }
                reach?.forEach { r ->
                    item(key = "on:" + r.machine) {
                        Text(
                            if (r.machine.isEmpty()) "On this phone" else "On ${r.machine}",
                            style = MaterialTheme.typography.titleSmall,
                            modifier = Modifier.padding(start = 24.dp, top = 12.dp, bottom = 4.dp),
                        )
                    }
                    when {
                        r.err.isNotEmpty() -> item(key = "err:" + r.machine) { Banner("Could not ask ${r.machine}: ${r.err}", error = true) }
                        !r.known -> item(key = "unknown:" + r.machine) {
                            Banner("${r.machine.ifEmpty { "This phone" }} has never met $name, so only what is Public opens for them there.")
                        }
                    }
                    items(r.paths, key = { "p:" + r.machine + ":" + it.path }) { p ->
                        Toggle(
                            title = p.path,
                            says = opensSays(p),
                            on = p.opens,
                            enabled = r.known && busy == null,
                        ) { open ->
                            busy = r.machine + p.path
                            scope.launch {
                                Drop.open(r.machine, p.path, r.called, open, p.at, name)
                                    .onSuccess { reach = it }
                                    .onFailure { said.showSnackbar(it.message ?: "Could not change it") }
                                busy = null
                            }
                        }
                    }
                }
            }
        }
    }

    renaming?.let { old ->
        Renaming(old, done = { renaming = null }) { called ->
            scope.launch {
                Drop.call { it.rename(old, called) }
                    .onSuccess { if (old == name) home() else Drop.bump() }
                    .onFailure { said.showSnackbar(it.message ?: "Could not rename") }
            }
        }
    }

    takingOut?.let { machine ->
        AlertDialog(
            onDismissRequest = { takingOut = null },
            title = { Text("Remove $machine from your machines?") },
            text = { Text("Every machine of yours turns it away from now on, whatever badge it still wears. Run drop machine add to bring it back.") },
            confirmButton = {
                TextButton(onClick = {
                    takingOut = null
                    scope.launch {
                        Drop.call { it.forget(machine) }
                            .onSuccess { said.showSnackbar("$machine is no longer one of your machines") }
                            .onFailure { said.showSnackbar(it.message ?: "Could not remove it") }
                        Drop.bump()
                    }
                }) { Text("Remove", color = MaterialTheme.colorScheme.error) }
            },
            dismissButton = { TextButton(onClick = { takingOut = null }) { Text("Cancel") } },
        )
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
                }) { Text("Remove", color = MaterialTheme.colorScheme.error) }
            },
            dismissButton = { TextButton(onClick = { removing = false }) { Text("Cancel") } },
        )
    }
}

/** What one path does for one person, in words: whether it opens, and what decides it. */
private fun opensSays(p: PathOpen): String {
    val by = when (p.at) {
        "allowed" -> "let in by name"
        "refused" -> "kept out by name"
        else -> stepOf(p.level).title
    }
    return (if (p.opens) "Opens · " else "Shut · ") + by
}

/** The ⋮ on a machine of yours: rename it, or take it out of your machines. */
@Composable
private fun MachineActions(rename: () -> Unit, remove: () -> Unit) {
    var open by remember { mutableStateOf(false) }
    Box {
        IconButton(onClick = { open = true }) { Icon(Icons.Filled.MoreVert, "Machine actions") }
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            DropdownMenuItem(text = { Text("Rename") }, onClick = { open = false; rename() })
            DropdownMenuItem(text = { Text("Remove from my machines") }, onClick = { open = false; remove() })
        }
    }
}

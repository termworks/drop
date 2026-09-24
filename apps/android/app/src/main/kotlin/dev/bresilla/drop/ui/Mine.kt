package dev.bresilla.drop.ui

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
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
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.QrCodeScanner
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Badge
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
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
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import dev.bresilla.drop.Drop
import dev.bresilla.drop.Knock
import dev.bresilla.drop.Machine
import dev.bresilla.drop.Me
import dev.bresilla.drop.PathState
import kotlinx.coroutines.launch

/** This phone and the other machines that are yours: what each shares, and who may open it. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MineTab(go: (Screen) -> Unit) {
    val scope = rememberCoroutineScope()
    val tick by Drop.tick.collectAsState()
    var me by remember { mutableStateOf<Me?>(null) }
    var machines by remember { mutableStateOf<List<Machine>>(emptyList()) }
    var here by remember { mutableStateOf<List<PathState>>(emptyList()) }
    var knocks by remember { mutableStateOf<List<Knock>>(emptyList()) }

    LaunchedEffect(tick) {
        me = Drop.self()
        machines = Drop.people().getOrNull()?.filter { it.me }?.flatMap { it.machines } ?: machines
        here = Drop.levels("").getOrNull() ?: here
        knocks = Drop.knocked().getOrNull() ?: knocks
    }

    Scaffold(
        contentWindowInsets = WindowInsets(0),
        topBar = {
            TopAppBar(
                title = { Text("My machines") },
                actions = { IconButton(onClick = { go(Screen.AddMachine) }) { Icon(Icons.Filled.Add, "Add a machine") } },
            )
        },
    ) { pad ->
        LazyColumn(contentPadding = PaddingValues(top = pad.calculateTopPadding(), bottom = 32.dp)) {
            item {
                val asked = here.sumOf { it.asked }
                ListItem(
                    modifier = Modifier
                        .padding(horizontal = 12.dp, vertical = 4.dp)
                        .clip(RoundedCornerShape(20.dp))
                        .clickable { go(Screen.Machine("")) },
                    colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.primaryContainer),
                    leadingContent = { Avatar(me?.name ?: "?", online = me != null) },
                    headlineContent = { Text(me?.name ?: "starting…", style = MaterialTheme.typography.titleMedium) },
                    supportingContent = {
                        Text("this phone · ${here.size} shared" + if (asked > 0) " · $asked asking" else "")
                    },
                    trailingContent = { Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, null) },
                )
            }

            item { Section("Your other machines") }
            if (machines.isEmpty()) {
                item {
                    Card(
                        modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp).fillMaxWidth(),
                        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
                        shape = RoundedCornerShape(20.dp),
                    ) {
                        Column(Modifier.padding(18.dp)) {
                            Text("Only this phone so far.", style = MaterialTheme.typography.titleMedium)
                            Spacer(Modifier.height(6.dp))
                            Text(
                                "Add a computer of yours and it shows up here, with every path it has and who may open it — changed from this phone.",
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            Spacer(Modifier.height(12.dp))
                            Button(onClick = { go(Screen.AddMachine) }) { Text("Add a machine") }
                        }
                    }
                }
            }
            items(machines, key = { "m:" + it.id }) { m ->
                Entry(m.name, m.brief + if (m.reaching) " · online" else "", m.reaching, false) { go(Screen.Machine(m.name)) }
            }

            val asking = here.filter { it.asked > 0 }
            if (asking.isNotEmpty()) {
                item { Section("Asking to be let in") }
                items(asking, key = { "a:" + it.path }) { p ->
                    ListItem(
                        modifier = Modifier
                            .padding(horizontal = 12.dp, vertical = 3.dp)
                            .clip(RoundedCornerShape(20.dp))
                            .clickable { go(Screen.Access("", p.path)) },
                        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.tertiaryContainer),
                        leadingContent = { KindBadge(p.archetype) },
                        headlineContent = { Text(p.path.trimStart('/'), style = MaterialTheme.typography.titleMedium) },
                        supportingContent = { Text("${p.asked} waiting for an answer") },
                        trailingContent = { Badge { Text("${p.asked}") } },
                    )
                }
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

/** The two ways a machine becomes yours: this phone joining, or another computer being added. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AddMachineScreen(go: (Screen) -> Unit, back: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var me by remember { mutableStateOf<Me?>(null) }
    var taking by remember { mutableStateOf(false) }
    var told by remember { mutableStateOf<String?>(null) }
    var failed by remember { mutableStateOf<String?>(null) }
    var pasting by remember { mutableStateOf(false) }

    LaunchedEffect(Unit) { me = Drop.self() }

    val take: (String) -> Unit = { code ->
        taking = true
        failed = null
        scope.launch {
            Drop.take(context.applicationContext, code)
                .onSuccess { told = it; me = Drop.self() }
                .onFailure { failed = it.message }
            taking = false
        }
    }
    val camera = rememberLauncherForActivityResult(ScanContract()) { result -> result.contents?.let(take) }
    val scan = {
        camera.launch(
            ScanOptions()
                .setDesiredBarcodeFormats(ScanOptions.QR_CODE)
                .setPrompt("Point at the code drop is showing")
                .setBeepEnabled(false)
                .setOrientationLocked(false),
        )
    }
    val phone = me?.name ?: "this-phone"

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Add a machine") },
                navigationIcon = { IconButton(onClick = back) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
            )
        },
    ) { pad ->
        LazyColumn(contentPadding = PaddingValues(top = pad.calculateTopPadding(), bottom = 32.dp)) {
            item {
                WayIn(
                    title = "Make this phone one of your machines",
                    says = "On a computer that has your key, run one of these and take the code it shows:",
                    commands = listOf(
                        "drop me user vouch $phone" to "your key stays on the computer",
                        "drop me user export" to "copies your key onto this phone",
                    ),
                ) {
                    Row(horizontalArrangement = Arrangement.spacedBy(10.dp), verticalAlignment = Alignment.CenterVertically) {
                        Button(onClick = scan, enabled = !taking) {
                            Icon(Icons.Filled.QrCodeScanner, null, Modifier.size(18.dp))
                            Spacer(Modifier.width(6.dp))
                            Text("Scan")
                        }
                        TextButton(onClick = { pasting = true }, enabled = !taking) { Text("Paste") }
                        if (taking) CircularProgressIndicator(Modifier.size(22.dp), strokeWidth = 2.dp)
                    }
                    told?.let { Banner(it, Modifier.padding(top = 8.dp)) }
                    failed?.let { Banner(it, Modifier.padding(top = 8.dp), error = true) }
                }
            }
            item {
                WayIn(
                    title = "Add another computer of yours",
                    says = "Pair with it first. If it already carries your key it lands here straight away; if it has a key of its own, a computer with yours vouches for it.",
                    commands = listOf(
                        "drop peer pair" to "on it, then scan what it shows",
                        "drop me user vouch <it>" to "on a computer with your key",
                        "drop me user take <code>" to "back on it, with that code",
                    ),
                ) {
                    Button(onClick = { go(Screen.Pair(scan = true)) }) {
                        Icon(Icons.Filled.QrCodeScanner, null, Modifier.size(18.dp))
                        Spacer(Modifier.width(6.dp))
                        Text("Scan its pairing code")
                    }
                }
            }
        }
    }

    if (pasting) {
        var pasted by remember { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { pasting = false },
            title = { Text("Paste the code") },
            text = { OutlinedTextField(pasted, { pasted = it }, placeholder = { Text("drop://badge/…") }, textStyle = Mono) },
            confirmButton = {
                TextButton(enabled = Drop.owning(pasted), onClick = { pasting = false; take(pasted) }) { Text("Take it") }
            },
            dismissButton = { TextButton(onClick = { pasting = false }) { Text("Cancel") } },
        )
    }
}

/** One way of adding a machine: what it does, the commands it takes, and what to press here. */
@Composable
private fun WayIn(title: String, says: String, commands: List<Pair<String, String>>, actions: @Composable () -> Unit) {
    Card(
        modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp).fillMaxWidth(),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
        shape = RoundedCornerShape(24.dp),
    ) {
        Column(Modifier.padding(20.dp)) {
            Text(title, style = MaterialTheme.typography.titleLarge)
            Spacer(Modifier.height(8.dp))
            Text(says, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(Modifier.height(10.dp))
            commands.forEach { (command, why) ->
                Text(command, style = Mono, color = MaterialTheme.colorScheme.primary)
                Text(why, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                Spacer(Modifier.height(6.dp))
            }
            Spacer(Modifier.height(8.dp))
            actions()
        }
    }
}

package dev.bresilla.drop.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.Devices
import androidx.compose.material.icons.filled.QrCode2
import androidx.compose.material.icons.filled.QrCodeScanner
import androidx.compose.material.icons.filled.Star
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LargeTopAppBar
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import dev.bresilla.drop.Drop
import dev.bresilla.drop.Me
import dev.bresilla.drop.Person

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PeopleTab(go: (Screen) -> Unit) {
    val tick by Drop.tick.collectAsState()
    val trouble by Drop.trouble.collectAsState()
    var me by remember { mutableStateOf<Me?>(null) }
    var people by remember { mutableStateOf<List<Person>?>(null) }
    var failed by remember { mutableStateOf<String?>(null) }

    LaunchedEffect(tick) {
        me = Drop.self()
        Drop.people()
            .onSuccess { people = it; failed = null }
            .onFailure { failed = it.message }
    }

    val scroll = TopAppBarDefaults.exitUntilCollapsedScrollBehavior()
    Scaffold(
        modifier = Modifier.nestedScroll(scroll.nestedScrollConnection),
        contentWindowInsets = WindowInsets(0),
        topBar = {
            LargeTopAppBar(
                title = { Text("drop") },
                scrollBehavior = scroll,
            )
        },
    ) { pad ->
        LazyColumn(contentPadding = PaddingValues(top = pad.calculateTopPadding(), bottom = 96.dp)) {
            item { Hero(me, go) }
            trouble?.let { text -> item { Box(Modifier.clickable { Drop.clearTrouble() }) { Banner(text, error = true) } } }
            failed?.let { text -> item { Banner(text, error = true) } }

            val all = people
            when {
                all == null -> item {
                    Box(Modifier.fillMaxWidth().padding(48.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
                }
                all.isEmpty() -> item { Nobody(go) }
                else -> {
                    val mine = all.filter { it.me }
                    val others = all.filter { !it.me }
                    if (mine.isNotEmpty()) {
                        item { Section("Your other machines") }
                        items(mine.flatMap { it.machines }, key = { "m:" + it.id }) { m ->
                            Entry(m.name, m.brief, m.reaching, m.trusted) { go(Screen.Machine(m.name)) }
                        }
                    }
                    if (others.isNotEmpty()) {
                        item { Section("People") }
                        items(others, key = { "p:" + it.name }) { p ->
                            val one = p.machines.singleOrNull()
                            val says = when {
                                one != null -> one.name + if (one.reaching) " · online" else ""
                                else -> "${p.machines.size} machines" + if (p.reaching > 0) " · ${p.reaching} online" else ""
                            }
                            Entry(if (p.anon) "on their own" else p.name, says, p.reaching > 0, p.trusted) {
                                go(if (one != null) Screen.Machine(one.name) else Screen.Person(p.name))
                            }
                        }
                    }
                }
            }
        }
    }
}

/** This device: what it is called, how to be found, and the two ways to pair from here. */
@Composable
private fun Hero(me: Me?, go: (Screen) -> Unit) {
    val scheme = MaterialTheme.colorScheme
    Box(
        modifier = Modifier
            .padding(horizontal = 16.dp, vertical = 8.dp)
            .fillMaxWidth()
            .clip(RoundedCornerShape(28.dp))
            .background(Brush.linearGradient(listOf(Color(0xFF6E56CF), Color(0xFF3E63DD)))),
    ) {
        Column(Modifier.padding(22.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(Modifier.size(10.dp).clip(CircleShape).background(if (me != null) Color(0xFF7CF2A0) else Color(0x66FFFFFF)))
                Spacer(Modifier.width(8.dp))
                Text(
                    if (me != null) "Reachable" else "Starting…",
                    style = MaterialTheme.typography.labelLarge,
                    color = Color.White.copy(alpha = 0.9f),
                )
            }
            Spacer(Modifier.height(10.dp))
            Text(
                me?.name ?: "this device",
                style = MaterialTheme.typography.headlineSmall,
                color = Color.White,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(me?.brief ?: "", style = Mono, color = Color.White.copy(alpha = 0.75f))
            Spacer(Modifier.height(18.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp), modifier = Modifier.fillMaxWidth()) {
                Button(
                    onClick = { go(Screen.Pair()) },
                    colors = ButtonDefaults.buttonColors(containerColor = Color.White, contentColor = Color(0xFF3E2FB0)),
                    contentPadding = PaddingValues(horizontal = 12.dp),
                    modifier = Modifier.weight(1f),
                ) {
                    Icon(Icons.Filled.QrCode2, null, Modifier.size(18.dp))
                    Spacer(Modifier.width(6.dp))
                    Text("My code", maxLines = 1)
                }
                FilledTonalButton(
                    onClick = { go(Screen.Pair(scan = true)) },
                    colors = ButtonDefaults.filledTonalButtonColors(containerColor = Color.White.copy(alpha = 0.2f), contentColor = Color.White),
                    contentPadding = PaddingValues(horizontal = 12.dp),
                    modifier = Modifier.weight(1f),
                ) {
                    Icon(Icons.Filled.QrCodeScanner, null, Modifier.size(18.dp))
                    Spacer(Modifier.width(6.dp))
                    Text("Scan", maxLines = 1)
                }
            }
        }
        Icon(
            Icons.Filled.Devices, null,
            tint = scheme.onPrimary.copy(alpha = 0.12f),
            modifier = Modifier.align(Alignment.TopEnd).padding(18.dp).size(72.dp),
        )
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
                "Pair once with a computer or a friend's phone, and you can reach each other from anywhere after that.",
                style = MaterialTheme.typography.bodyMedium,
                textAlign = TextAlign.Center,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(16.dp))
            Text(
                "On a computer: drop peer pair",
                style = Mono,
                color = MaterialTheme.colorScheme.primary,
            )
            Spacer(Modifier.height(16.dp))
            Button(onClick = { go(Screen.Pair(scan = true)) }) { Text("Scan its code") }
        }
    }
}

@Composable
private fun Entry(title: String, subtitle: String, online: Boolean, trusted: Boolean, onClick: () -> Unit) {
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

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PersonScreen(name: String, go: (Screen) -> Unit, back: () -> Unit) {
    val tick by Drop.tick.collectAsState()
    var person by remember { mutableStateOf<Person?>(null) }
    LaunchedEffect(tick) { person = Drop.people().getOrNull()?.firstOrNull { it.name == name } }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(name) },
                navigationIcon = { IconButton(onClick = back) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
            )
        },
    ) { pad ->
        LazyColumn(Modifier.fillMaxSize(), contentPadding = pad) {
            item { Section("Machines") }
            items(person?.machines ?: emptyList(), key = { it.id }) { m ->
                Entry(m.name, m.brief + if (m.reaching) " · online" else "", m.reaching, m.trusted) { go(Screen.Machine(m.name)) }
            }
        }
    }
}

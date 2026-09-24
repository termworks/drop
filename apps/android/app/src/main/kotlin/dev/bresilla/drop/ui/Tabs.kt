package dev.bresilla.drop.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Chat
import androidx.compose.material.icons.automirrored.filled.InsertDriveFile
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.Group
import androidx.compose.material.icons.filled.Link
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.Devices
import androidx.compose.material.icons.filled.Settings as SettingsIcon
import androidx.compose.material.icons.filled.Schedule
import androidx.compose.material3.Badge
import androidx.compose.material3.BadgedBox
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import dev.bresilla.drop.Chat
import dev.bresilla.drop.Drop
import dev.bresilla.drop.Kept
import dev.bresilla.drop.Knock
import dev.bresilla.drop.Me
import dev.bresilla.drop.Rule
import dev.bresilla.drop.Served
import dev.bresilla.drop.Settings
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** The four places everything starts from: conversations, other people, your own machines, and this phone's settings. */
@Composable
fun HomeScreen(tab: Int, onTab: (Int) -> Unit, go: (Screen) -> Unit) {
    val context = LocalContext.current
    val tick by Drop.tick.collectAsState()
    var unread by remember { mutableIntStateOf(0) }

    LaunchedEffect(tick) {
        unread = Drop.conversations().getOrNull()?.sumOf { unreadIn(context, it) } ?: 0
    }
    Pulse()

    Scaffold(
        bottomBar = {
            NavigationBar {
                NavigationBarItem(
                    selected = tab == 0,
                    onClick = { onTab(0) },
                    icon = {
                        BadgedBox(badge = { if (unread > 0) Badge { Text(if (unread > 99) "99+" else "$unread") } }) {
                            Icon(Icons.AutoMirrored.Filled.Chat, null)
                        }
                    },
                    label = { Text("Chats") },
                )
                NavigationBarItem(
                    selected = tab == 1,
                    onClick = { onTab(1) },
                    icon = { Icon(Icons.Filled.Group, null) },
                    label = { Text("People") },
                )
                NavigationBarItem(
                    selected = tab == 2,
                    onClick = { onTab(2) },
                    icon = { Icon(Icons.Filled.Devices, null) },
                    label = { Text("Mine") },
                )
                NavigationBarItem(
                    selected = tab == 3,
                    onClick = { onTab(3) },
                    icon = { Icon(Icons.Filled.SettingsIcon, null) },
                    label = { Text("Settings") },
                )
            }
        },
    ) { pad ->
        Box(Modifier.fillMaxSize().padding(bottom = pad.calculateBottomPadding())) {
            when (tab) {
                0 -> ChatsTab(go, onPeople = { onTab(1) })
                1 -> PeopleTab(go)
                2 -> MineTab(go)
                else -> SettingsTab(go)
            }
        }
    }
}

/** How many of a conversation's messages arrived since it was last looked at. */
fun unreadIn(context: android.content.Context, chat: Chat): Int {
    val seen = Settings.seen(context, chat.machine)
    return chat.arrivals.count { it > seen }
}

private val clockTime = SimpleDateFormat("HH:mm", Locale.getDefault())
private val dayTime = SimpleDateFormat("d MMM", Locale.getDefault())

/** When something was said, the way a list of conversations shows it: a time today, a day otherwise. */
fun whenSaid(at: Long): String {
    val today = System.currentTimeMillis() - at < 20 * 60 * 60 * 1000
    return if (today) clockTime.format(Date(at)) else dayTime.format(Date(at))
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ChatsTab(go: (Screen) -> Unit, onPeople: () -> Unit) {
    val context = LocalContext.current
    val tick by Drop.tick.collectAsState()
    var chats by remember { mutableStateOf<List<Chat>?>(null) }
    var online by remember { mutableStateOf<Set<String>>(emptySet()) }

    LaunchedEffect(tick) {
        chats = Drop.conversations().getOrNull() ?: chats
        online = Drop.people().getOrNull()?.flatMap { it.machines }?.filter { it.reaching }?.map { it.name }?.toSet() ?: online
    }

    Scaffold(
        contentWindowInsets = WindowInsets(0),
        topBar = { TopAppBar(title = { Text("Chats") }) },
    ) { pad ->
        val all = chats
        when {
            all == null -> Box(Modifier.fillMaxSize().padding(pad), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
            all.isEmpty() -> Column(
                Modifier.fillMaxSize().padding(pad).padding(32.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.Center,
            ) {
                Text("No conversations yet", style = MaterialTheme.typography.titleLarge)
                Spacer(Modifier.height(8.dp))
                Text(
                    "Everything said with somebody is kept here, on this phone and theirs, and nowhere else.",
                    textAlign = TextAlign.Center,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(16.dp))
                Button(onClick = onPeople) { Text("Pick somebody") }
            }
            else -> LazyColumn(contentPadding = PaddingValues(top = pad.calculateTopPadding(), bottom = 24.dp)) {
                items(all, key = { it.machine }) { c ->
                    val unread = unreadIn(context, c)
                    ListItem(
                        modifier = Modifier
                            .padding(horizontal = 12.dp, vertical = 2.dp)
                            .clip(RoundedCornerShape(20.dp))
                            .clickable { go(Screen.Chat(c.machine)) },
                        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
                        leadingContent = { Avatar(c.machine, online = c.machine in online) },
                        headlineContent = { Text(c.machine, style = MaterialTheme.typography.titleMedium) },
                        supportingContent = { Preview(c) },
                        trailingContent = {
                            Column(horizontalAlignment = Alignment.End) {
                                Text(
                                    whenSaid(c.last.at),
                                    style = MaterialTheme.typography.labelSmall,
                                    color = if (unread > 0) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                                Spacer(Modifier.height(4.dp))
                                if (unread > 0) Badge { Text(if (unread > 99) "99+" else "$unread") }
                            }
                        },
                    )
                }
            }
        }
    }
}

/** The last thing said, as a line: who said it, and what — a file and a link shown as such. */
@Composable
private fun Preview(c: Chat) {
    val m = c.last
    Row(verticalAlignment = Alignment.CenterVertically) {
        when {
            m.out && m.waiting -> Icon(Icons.Filled.Schedule, "waiting", Modifier.size(14.dp), tint = MaterialTheme.colorScheme.outline)
            m.kind == "file" -> Icon(Icons.AutoMirrored.Filled.InsertDriveFile, null, Modifier.size(14.dp), tint = MaterialTheme.colorScheme.outline)
            m.kind == "link" -> Icon(Icons.Filled.Link, null, Modifier.size(14.dp), tint = MaterialTheme.colorScheme.outline)
        }
        if (m.out && m.waiting || m.kind == "file" || m.kind == "link") Spacer(Modifier.width(4.dp))
        Text(
            (if (m.out) "You: " else "") + m.body,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
fun Toggle(title: String, says: String, on: Boolean, enabled: Boolean = true, onChange: (Boolean) -> Unit) {
    ListItem(
        modifier = Modifier
            .padding(horizontal = 12.dp, vertical = 3.dp)
            .clip(RoundedCornerShape(20.dp))
            .clickable(enabled = enabled) { onChange(!on) },
        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
        headlineContent = { Text(title) },
        supportingContent = { Text(says, color = MaterialTheme.colorScheme.onSurfaceVariant) },
        trailingContent = { Switch(checked = on, onCheckedChange = onChange, enabled = enabled) },
    )
}

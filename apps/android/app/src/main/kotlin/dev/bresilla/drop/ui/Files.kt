package dev.bresilla.drop.ui

import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.InsertDriveFile
import androidx.compose.material.icons.filled.CreateNewFolder
import androidx.compose.material.icons.filled.DownloadForOffline
import androidx.compose.material.icons.filled.FileDownload
import androidx.compose.material.icons.filled.FileUpload
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.LinkOff
import androidx.compose.material.icons.filled.PhoneAndroid
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
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
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import dev.bresilla.drop.Drop
import dev.bresilla.drop.Held
import dev.bresilla.drop.Kept
import java.io.File
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

@OptIn(ExperimentalMaterial3Api::class, ExperimentalFoundationApi::class)
@Composable
fun FilesScreen(at: Screen.Files, go: (Screen) -> Unit, back: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val said = remember { SnackbarHostState() }

    var held by remember { mutableStateOf<List<Held>?>(null) }
    var failed by remember { mutableStateOf<String?>(null) }
    var fetching by remember { mutableStateOf<String?>(null) }
    var again by remember { mutableIntStateOf(0) }
    // acting is the row a long press chose, and naming is a name being typed: a new folder, or a new
    // name for acting.
    var acting by remember { mutableStateOf<Held?>(null) }
    var naming by remember { mutableStateOf<Held?>(null) }
    var making by remember { mutableStateOf(false) }
    var taking by remember { mutableStateOf(false) }
    var letting by remember { mutableStateOf(false) }

    val tick by Drop.tick.collectAsState()
    var kept by remember { mutableStateOf<List<Kept>>(emptyList()) }
    LaunchedEffect(tick) { kept = Drop.kept().getOrNull() ?: kept }
    val copied = kept.firstOrNull { at.shared.isNotEmpty() && it.shared == at.shared }

    LaunchedEffect(at, again) {
        failed = null
        Drop.list(at.machine, at.path, at.dir).onSuccess { held = it }.onFailure { failed = it.message }
    }

    // A copy this phone keeps is changed on its own disk, and the copy sends the change on from there.
    val here = at.local.isNotEmpty()
    fun inside(name: String) = File(File(at.local, at.dir), name)
    suspend fun onDisk(what: () -> Boolean): Result<Unit> = withContext(Dispatchers.IO) {
        runCatching { check(what()) { "this phone would not do that" } }
    }

    val pick = rememberLauncherForActivityResult(ActivityResultContracts.GetMultipleContents()) { uris: List<Uri> ->
        if (uris.isEmpty()) return@rememberLauncherForActivityResult
        scope.launch {
            val staged = stage(context, uris)
            for (file in staged) {
                (if (here) onDisk { file.copyTo(inside(file.name)).exists() } else Drop.call { it.put(at.machine, at.path, at.dir, file.absolutePath) })
                    .onFailure { said.showSnackbar("${file.name}: ${it.message}") }
                file.delete()
            }
            again++
        }
    }

    val where = (at.path.trimEnd('/') + "/" + at.dir).trimEnd('/').ifEmpty { "/" }
    val on = at.machine.ifEmpty { "this phone" }
    Scaffold(
        snackbarHost = { SnackbarHost(said) },
        topBar = {
            TopAppBar(
                title = {
                    Column {
                        Text(where.substringAfterLast('/').ifEmpty { at.path })
                        Text(if (here) "the copy on this phone" else "${at.machine}:$where", style = Mono, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                },
                navigationIcon = { IconButton(onClick = back) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
                actions = {
                    when {
                        here -> if (at.dir.isEmpty()) IconButton(onClick = { letting = true }) { Icon(Icons.Filled.LinkOff, "Stop keeping it level") }
                        copied != null -> IconButton(onClick = { go(Screen.Files("", copied.path, "", true, copied.shared, copied.where)) }) {
                            Icon(Icons.Filled.PhoneAndroid, "The copy on this phone")
                        }
                        at.shared.isNotEmpty() -> IconButton(enabled = !taking, onClick = {
                            taking = true
                            scope.launch {
                                Drop.call { it.hold(at.machine, at.path, "files") }
                                    .onSuccess { said.showSnackbar("Kept on this phone, in Download/drop-kept") }
                                    .onFailure { said.showSnackbar(it.message ?: "Could not keep a copy") }
                                Drop.bump()
                                taking = false
                            }
                        }) {
                            if (taking) CircularProgressIndicator(Modifier.padding(4.dp), strokeWidth = 2.dp)
                            else Icon(Icons.Filled.DownloadForOffline, "Keep a copy on this phone")
                        }
                    }
                    if (at.writable) {
                        IconButton(onClick = { making = true }) { Icon(Icons.Filled.CreateNewFolder, "New folder") }
                    }
                    IconButton(onClick = { again++ }) { Icon(Icons.Filled.Refresh, "Reload") }
                },
            )
        },
        floatingActionButton = {
            if (at.writable) {
                ExtendedFloatingActionButton(
                    onClick = { pick.launch("*/*") },
                    icon = { Icon(Icons.Filled.FileUpload, null) },
                    text = { Text("Upload here") },
                )
            }
        },
    ) { pad ->
        LazyColumn(contentPadding = PaddingValues(top = pad.calculateTopPadding(), bottom = 96.dp)) {
            item { Transfer() }
            failed?.let { item { Banner("Could not open $where on $on: $it", error = true) } }
            val all = held
            when {
                all == null && failed == null -> item {
                    Box(Modifier.fillMaxWidth().padding(32.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
                }
                all != null && all.isEmpty() -> item { Banner("Nothing in here.") }
            }
            items(all ?: emptyList(), key = { it.name }) { h ->
                ListItem(
                    modifier = Modifier
                        .padding(horizontal = 12.dp, vertical = 2.dp)
                        .clip(RoundedCornerShape(16.dp))
                        .combinedClickable(
                            enabled = fetching == null,
                            onLongClick = { acting = h },
                        ) {
                            if (h.dir) {
                                val inner = if (at.dir.isEmpty()) h.name else at.dir.trimEnd('/') + "/" + h.name
                                go(at.copy(dir = inner))
                            } else if (here) {
                                open(context, inside(h.name))
                            } else {
                                fetching = h.name
                                scope.launch {
                                    Drop.call { it.fetch(at.machine, at.path, at.dir, h.name) }
                                        .onSuccess { open(context, File(it)) }
                                        .onFailure { said.showSnackbar("${h.name}: ${it.message}") }
                                    fetching = null
                                }
                            }
                        },
                    colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
                    leadingContent = {
                        Icon(
                            if (h.dir) Icons.Filled.Folder else Icons.AutoMirrored.Filled.InsertDriveFile,
                            null,
                            tint = if (h.dir) look("files").tint else MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    },
                    headlineContent = { Text(h.name) },
                    supportingContent = {
                        Text(
                            listOfNotNull(if (h.dir) null else size(context, h.size), if (h.at > 0) ago(h.at) else null).joinToString(" · "),
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    },
                    trailingContent = {
                        when {
                            fetching == h.name -> CircularProgressIndicator(Modifier.padding(4.dp), strokeWidth = 2.dp)
                            !h.dir && !here -> Icon(Icons.Filled.FileDownload, "Download", tint = MaterialTheme.colorScheme.outline)
                        }
                    },
                )
            }
        }
    }

    acting?.let { h ->
        AlertDialog(
            onDismissRequest = { acting = null },
            title = { Text(h.name) },
            text = { Text(if (h.dir) "A folder on $on." else "${size(context, h.size)} on $on.") },
            confirmButton = {
                Row {
                    if (at.writable) {
                        TextButton(onClick = { acting = null; naming = h }) { Text("Rename") }
                        TextButton(onClick = {
                            acting = null
                            scope.launch {
                                (if (here) onDisk { inside(h.name).deleteRecursively() } else Drop.call { it.remove(at.machine, at.path, at.dir, h.name) })
                                    .onFailure { said.showSnackbar("${h.name}: ${it.message}") }
                                again++
                            }
                        }) { Text("Delete") }
                    }
                    if (!h.dir && !here) {
                        TextButton(onClick = {
                            acting = null
                            fetching = h.name
                            scope.launch {
                                Drop.call { it.fetch(at.machine, at.path, at.dir, h.name) }
                                    .onSuccess { said.showSnackbar("Saved to Download/drop") }
                                    .onFailure { said.showSnackbar("${h.name}: ${it.message}") }
                                fetching = null
                            }
                        }) { Text("Download") }
                    }
                }
            },
            dismissButton = { TextButton(onClick = { acting = null }) { Text("Close") } },
        )
    }

    naming?.let { h ->
        Named(title = "Rename ${h.name}", start = h.name, done = { naming = null }) { called ->
            scope.launch {
                (if (here) onDisk { inside(h.name).renameTo(inside(called)) } else Drop.call { it.move(at.machine, at.path, at.dir, h.name, called) })
                    .onFailure { said.showSnackbar("${h.name}: ${it.message}") }
                again++
            }
        }
    }

    if (letting) {
        AlertDialog(
            onDismissRequest = { letting = false },
            title = { Text("Stop keeping ${at.path.trimStart('/')} level?") },
            text = { Text("What is in ${at.local} stays on this phone, and stops following the others.") },
            confirmButton = {
                TextButton(onClick = {
                    letting = false
                    scope.launch {
                        Drop.call { it.release(at.path) }
                            .onSuccess { Drop.bump(); back() }
                            .onFailure { said.showSnackbar(it.message ?: "") }
                    }
                }) { Text("Stop") }
            },
            dismissButton = { TextButton(onClick = { letting = false }) { Text("Cancel") } },
        )
    }

    if (making) {
        Named(title = "New folder", start = "", done = { making = false }) { called ->
            scope.launch {
                (if (here) onDisk { inside(called).mkdirs() } else Drop.call { it.mkdir(at.machine, at.path, at.dir, called) })
                    .onFailure { said.showSnackbar("$called: ${it.message}") }
                again++
            }
        }
    }
}

/** A name to type: a folder to make, or a new name for something. */
@Composable
private fun Named(title: String, start: String, done: () -> Unit, use: (String) -> Unit) {
    // The name without its extension comes up chosen, so typing replaces the part that is the name.
    val stem = start.substringBeforeLast('.').ifEmpty { start }.length
    var name by remember { mutableStateOf(TextFieldValue(start, TextRange(0, stem))) }
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { focus.requestFocus() }
    AlertDialog(
        onDismissRequest = done,
        title = { Text(title) },
        text = {
            OutlinedTextField(
                name, { name = it },
                singleLine = true,
                label = { Text("Name") },
                modifier = Modifier.focusRequester(focus),
            )
        },
        confirmButton = {
            val typed = name.text.trim()
            TextButton(enabled = typed.isNotBlank() && !typed.contains('/'), onClick = { done(); use(typed) }) { Text("OK") }
        },
        dismissButton = { TextButton(onClick = done) { Text("Cancel") } },
    )
}

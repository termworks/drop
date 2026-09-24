package dev.bresilla.drop.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.ContentCopy
import androidx.compose.material.icons.filled.Done
import androidx.compose.material.icons.filled.DownloadForOffline
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
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
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.unit.dp
import dev.bresilla.drop.Drop
import dev.bresilla.drop.Kept
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * A shared note. Read as the machine that holds it has it, or — once this phone keeps a copy —
 * written in here, and the copy is kept level with everybody else's.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NoteScreen(at: Screen.Note, back: () -> Unit) {
    val tick by Drop.tick.collectAsState()
    var kept by remember { mutableStateOf<List<Kept>?>(null) }
    LaunchedEffect(tick) { kept = Drop.kept().getOrNull() ?: kept }

    val all = kept
    val held = all?.firstOrNull {
        if (at.machine.isEmpty()) it.path == at.path else at.shared.isNotEmpty() && it.shared == at.shared
    }
    when {
        all == null -> Box(Modifier.fillMaxSize()) { CircularProgressIndicator(Modifier.align(Alignment.Center)) }
        held != null -> Writing(at, held, back)
        at.machine.isEmpty() -> Box(Modifier.fillMaxSize()) { Banner("This phone keeps no copy of ${at.path} any more.") }
        else -> Reading(at, back)
    }
}

/** The note as the far end has it, and the way to keep a copy of it here. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun Reading(at: Screen.Note, back: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val said = remember { SnackbarHostState() }
    var body by remember { mutableStateOf<String?>(null) }
    var failed by remember { mutableStateOf<String?>(null) }
    var again by remember { mutableIntStateOf(0) }
    var taking by remember { mutableStateOf(false) }

    LaunchedEffect(again) {
        failed = null
        Drop.call { it.note(at.machine, at.path) }.onSuccess { body = it }.onFailure { failed = it.message }
    }

    Scaffold(
        snackbarHost = { SnackbarHost(said) },
        topBar = {
            TopAppBar(
                title = {
                    Column {
                        Text(at.path.trimStart('/'))
                        Text("${at.machine} · a note several people write", style = Mono, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                },
                navigationIcon = { IconButton(onClick = back) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
                actions = {
                    body?.let { text -> IconButton(onClick = { copy(context, at.path, text) }) { Icon(Icons.Filled.ContentCopy, "Copy") } }
                    IconButton(onClick = { again++ }) { Icon(Icons.Filled.Refresh, "Read again") }
                },
            )
        },
        floatingActionButton = {
            if (at.shared.isNotEmpty()) {
                ExtendedFloatingActionButton(
                    onClick = {
                        if (taking) return@ExtendedFloatingActionButton
                        taking = true
                        scope.launch {
                            Drop.call { it.hold(at.machine, at.path, "note") }
                                .onFailure { said.showSnackbar(it.message ?: "Could not keep a copy") }
                            Drop.bump()
                            taking = false
                        }
                    },
                    icon = {
                        if (taking) CircularProgressIndicator(Modifier.padding(2.dp), strokeWidth = 2.dp)
                        else Icon(Icons.Filled.DownloadForOffline, null)
                    },
                    text = { Text("Keep a copy to write in") },
                )
            }
        },
    ) { pad ->
        Box(Modifier.fillMaxSize().padding(pad)) {
            val text = body
            when {
                failed != null -> Banner("Could not read it: $failed", error = true)
                text == null -> CircularProgressIndicator(Modifier.align(Alignment.Center))
                else -> Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp)) {
                    Surface(
                        color = MaterialTheme.colorScheme.surfaceContainerLow,
                        shape = RoundedCornerShape(16.dp),
                        modifier = Modifier.fillMaxWidth(),
                    ) {
                        SelectionContainer {
                            Text(
                                text.ifEmpty { "(empty)" },
                                fontFamily = FontFamily.Monospace,
                                style = MaterialTheme.typography.bodyMedium,
                                modifier = Modifier.padding(16.dp),
                            )
                        }
                    }
                    Banner(
                        if (at.shared.isNotEmpty()) "Keeping a copy makes this phone one of the machines holding it: what is written here goes to the others, and theirs comes here."
                        else "${at.machine} holds this note alone, so it is written in there.",
                    )
                }
            }
        }
    }
}

/** The copy this phone keeps, to write in. What arrives while nothing is typed is shown at once. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun Writing(at: Screen.Note, mine: Kept, back: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val said = remember { SnackbarHostState() }
    // base is the note as it was when the typing started, and what a save is merged against.
    var base by remember { mutableStateOf<String?>(null) }
    var text by remember { mutableStateOf(TextFieldValue("")) }
    var came by remember { mutableStateOf(false) }
    var saving by remember { mutableStateOf(false) }
    var menu by remember { mutableStateOf(false) }
    val dirty = base != null && text.text != base

    LaunchedEffect(mine.path) {
        while (true) {
            Drop.call { it.written(mine.path) }.onSuccess { now ->
                val was = base
                when {
                    was == null || text.text == was -> {
                        base = now
                        if (text.text != now) text = TextFieldValue(now, TextRange(minOf(text.selection.end, now.length)))
                        came = false
                    }
                    now != was -> came = true
                }
            }
            delay(1500)
        }
    }

    fun save(then: () -> Unit = {}) {
        val from = base ?: return then()
        if (text.text == from) return then()
        saving = true
        scope.launch {
            Drop.call { it.write(mine.path, from, text.text) }
                .onSuccess { merged ->
                    base = merged
                    text = TextFieldValue(merged, TextRange(minOf(text.selection.end, merged.length)))
                    came = false
                    then()
                }
                .onFailure { said.showSnackbar("Not saved: ${it.message}") }
            saving = false
        }
    }
    val leave = { save(back) }
    BackHandler(enabled = dirty) { leave() }

    Scaffold(
        snackbarHost = { SnackbarHost(said) },
        topBar = {
            TopAppBar(
                title = {
                    Column {
                        Text(mine.path.trimStart('/'))
                        Text(
                            if (dirty) "not saved yet" else "kept on this phone · level with the others",
                            style = Mono,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                },
                navigationIcon = { IconButton(onClick = leave) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
                actions = {
                    if (saving) CircularProgressIndicator(Modifier.padding(12.dp), strokeWidth = 2.dp)
                    else IconButton(onClick = { save() }, enabled = dirty) { Icon(Icons.Filled.Done, "Save") }
                    IconButton(onClick = { menu = true }) { Icon(Icons.Filled.MoreVert, "More") }
                    DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                        DropdownMenuItem(text = { Text("Copy all") }, onClick = { menu = false; copy(context, mine.path, text.text) })
                        DropdownMenuItem(
                            text = { Text("Stop keeping a copy") },
                            onClick = {
                                menu = false
                                scope.launch {
                                    Drop.call { it.release(mine.path) }.onFailure { said.showSnackbar(it.message ?: "") }
                                    Drop.bump()
                                    if (at.machine.isEmpty()) back()
                                }
                            },
                        )
                    }
                },
            )
        },
    ) { pad ->
        Column(Modifier.fillMaxSize().padding(pad).imePadding().padding(horizontal = 12.dp)) {
            if (came) Banner("It changed elsewhere while you were writing. Saving keeps both.")
            if (base == null) {
                Box(Modifier.fillMaxSize()) { CircularProgressIndicator(Modifier.align(Alignment.Center)) }
            } else {
                Surface(
                    color = MaterialTheme.colorScheme.surfaceContainerLow,
                    shape = RoundedCornerShape(16.dp),
                    modifier = Modifier.fillMaxSize().padding(bottom = 12.dp),
                ) {
                    BasicTextField(
                        value = text,
                        onValueChange = { text = it },
                        textStyle = MaterialTheme.typography.bodyMedium.copy(
                            fontFamily = FontFamily.Monospace,
                            color = MaterialTheme.colorScheme.onSurface,
                        ),
                        cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
                        modifier = Modifier.fillMaxSize().padding(16.dp),
                    )
                }
            }
        }
    }
}

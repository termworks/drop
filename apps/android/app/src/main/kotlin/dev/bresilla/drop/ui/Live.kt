package dev.bresilla.drop.ui

import android.app.Activity
import android.content.res.Configuration
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.isImeVisible
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Keyboard
import androidx.compose.material.icons.filled.Remove
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.view.WindowCompat
import dev.bresilla.drop.Drop
import dev.bresilla.drop.Settings
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch
import mobile.Live

private val Ink = Color(Grid.INK)
private val Ground = Color(Grid.GROUND)

/**
 * A live path: a terminal to watch or type into, or a command's output as it comes. The phone's own
 * grid is what it tells the far end it is, again whenever the keyboard comes or goes or the text is
 * pinched to another size.
 */
@OptIn(ExperimentalMaterial3Api::class, androidx.compose.foundation.layout.ExperimentalLayoutApi::class)
@Composable
fun LiveScreen(at: Screen.Live, back: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val grids = remember { MutableStateFlow<Grid?>(null) }
    val ends = remember { MutableStateFlow<String?>(null) }
    val companies = remember { MutableStateFlow<Pair<Long, Boolean>?>(null) }
    val grid by grids.collectAsState()
    val ended by ends.collectAsState()
    val company by companies.collectAsState()
    var live by remember { mutableStateOf<Live?>(null) }
    var view by remember { mutableStateOf<TermView?>(null) }
    var size by remember { mutableStateOf(0 to 0) }
    var held by remember { mutableStateOf(false to false) }
    var textSp by remember { mutableStateOf(0f) }
    val keyboard = WindowInsets.isImeVisible
    LightBars()

    DisposableEffect(at) {
        val screen = object : mobile.Screen {
            override fun drawn(frame: String) {
                grids.value = runCatching { Grid.parse(frame) }.getOrNull() ?: grids.value
            }

            override fun ended(why: String) {
                ends.value = why.ifEmpty { "it ended" }
            }

            override fun company(watching: Long, own: Boolean) {
                companies.value = watching to own
            }
        }
        var started: Live? = null
        val job = scope.launch {
            val (cols, rows) = size.takeIf { it.first > 0 } ?: (80 to 24)
            Drop.call { it.watch(at.machine, at.path, at.archetype, cols.toLong(), rows.toLong(), screen) }
                .onSuccess {
                    started = it
                    live = it
                    val (c, r) = size
                    if (c > 0) Drop.call { _ -> it.resize(c.toLong(), r.toLong()) }
                }
                .onFailure { ends.value = it.message ?: "could not open it" }
        }
        onDispose {
            job.cancel()
            started?.stop()
        }
    }

    // Every size this view settles on goes to the far end; a shared terminal takes the smallest of
    // the windows it is shown in, and a command's output is drawn at it here.
    val resize: (Int, Int) -> Unit = { cols, rows ->
        size = cols to rows
        live?.let { talking -> scope.launch { Drop.call { talking.resize(cols.toLong(), rows.toLong()) } } }
    }

    // Sideways, every row counts: the title goes, and back is the system's own gesture.
    val sideways = LocalConfiguration.current.orientation == Configuration.ORIENTATION_LANDSCAPE
    Scaffold(
        containerColor = Ground,
        topBar = {
            if (!sideways) TopAppBar(
                title = {
                    Column {
                        Text(at.path.trimStart('/'))
                        Text(describe(at, company, grid), style = Mono, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                },
                navigationIcon = { IconButton(onClick = { view?.hideKeyboard(); back() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
                actions = { TermSettings(at.typing, view, textSp, keyboard) { textSp = it } },
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = Ground,
                    titleContentColor = Ink,
                    navigationIconContentColor = Ink,
                    actionIconContentColor = Ink,
                ),
            )
        },
    ) { pad ->
        Column(Modifier.fillMaxSize().padding(pad).background(Ground).navigationBarsPadding().imePadding()) {
            Box(Modifier.weight(1f).fillMaxWidth()) {
                if (sideways) {
                    Box(Modifier.align(Alignment.TopEnd).padding(4.dp).background(Color(0xCC17171D), CircleShape)) {
                        CompositionLocalProvider(LocalContentColor provides Ink) {
                            TermSettings(at.typing, view, textSp, keyboard) { textSp = it }
                        }
                    }
                }
                AndroidView(
                    factory = { ctx ->
                        TermView(ctx).also { made ->
                            Settings.termSize(ctx).takeIf { it > 0 }?.let { made.textSize = it }
                            made.typing = at.typing
                            made.onSize = resize
                            made.onType = { text -> live?.let { talking -> scope.launch { Drop.call { talking.type(text) } } } }
                            made.onModifiers = { ctrl, alt -> held = ctrl to alt }
                            made.onTextSize = { Settings.termSized(ctx, it); textSp = made.textSp }
                            textSp = made.textSp
                            view = made
                            if (at.typing) made.post { made.showKeyboard() }
                        }
                    },
                    update = { v -> v.grid = grid },
                    modifier = Modifier.fillMaxSize().padding(2.dp),
                )
                if (grid == null) {
                    Text(
                        ended ?: "connecting…",
                        color = Ink,
                        fontFamily = FontFamily.Monospace,
                        modifier = Modifier.align(Alignment.Center).padding(16.dp),
                    )
                }
            }
            if (grid != null) ended?.let {
                Text("— $it", color = Color(0xFFFF7B72), fontFamily = FontFamily.Monospace, fontSize = 12.sp, modifier = Modifier.padding(6.dp))
            }
            if (at.typing) ExtraKeys(view, held)
        }
    }
}

/** A key on the extra rows: what it says, and what it sends — typed like a letter, so a held CTRL or ALT
 * changes it, or sent as a sequence of its own — or which modifier it holds. */
private data class Extra(val label: String, val sends: String = "", val holds: String = "", val typed: Boolean = false)

/** Termux's two rows: what a phone keyboard lacks and a terminal needs, a thumb away. */
private val Rows = listOf(
    listOf(Extra("ESC", "\u001b"), Extra("/", "/", typed = true), Extra("-", "-", typed = true), Extra("HOME", "\u001b[H"), Extra("↑", "\u001b[A"), Extra("END", "\u001b[F"), Extra("PGUP", "\u001b[5~")),
    listOf(Extra("TAB", "\t"), Extra("CTRL", holds = "ctrl"), Extra("ALT", holds = "alt"), Extra("←", "\u001b[D"), Extra("↓", "\u001b[B"), Extra("→", "\u001b[C"), Extra("PGDN", "\u001b[6~")),
)

@Composable
private fun ExtraKeys(view: TermView?, held: Pair<Boolean, Boolean>) {
    Column(Modifier.fillMaxWidth().background(Color(0xFF17171D))) {
        for (row in Rows) {
            Row(Modifier.fillMaxWidth()) {
                for (k in row) {
                    val on = (k.holds == "ctrl" && held.first) || (k.holds == "alt" && held.second)
                    Box(
                        contentAlignment = Alignment.Center,
                        modifier = Modifier
                            .weight(1f)
                            .height(40.dp)
                            .background(if (on) Color(0xFF3E63DD) else Color.Transparent)
                            .clickable {
                                val v = view ?: return@clickable
                                when {
                                    k.holds.isNotEmpty() -> v.toggle(control = k.holds == "ctrl")
                                    k.typed -> v.type(k.sends)
                                    else -> v.key(k.sends)
                                }
                            },
                    ) {
                        Text(k.label, color = Ink, fontFamily = FontFamily.Monospace, fontSize = 13.sp, textAlign = TextAlign.Center)
                    }
                }
            }
        }
    }
}

/** Light status and navigation icons while a dark screen is up, and whatever there was before after it. */
@Composable
private fun LightBars() {
    val view = LocalView.current
    DisposableEffect(view) {
        val window = (view.context as? Activity)?.window ?: return@DisposableEffect onDispose {}
        val bars = WindowCompat.getInsetsController(window, view)
        val status = bars.isAppearanceLightStatusBars
        val navigation = bars.isAppearanceLightNavigationBars
        bars.isAppearanceLightStatusBars = false
        bars.isAppearanceLightNavigationBars = false
        onDispose {
            bars.isAppearanceLightStatusBars = status
            bars.isAppearanceLightNavigationBars = navigation
        }
    }
}

/** The line under a terminal's name: whose machine, whose shell, and its shape. */
private fun describe(at: Screen.Live, company: Pair<Long, Boolean>?, grid: Grid?): String {
    val parts = mutableListOf(at.machine)
    company?.let { (watching, own) ->
        parts += when {
            own -> "your own shell"
            watching > 1 -> "shared, $watching watching"
            else -> "shared, only you"
        }
    }
    grid?.let { parts += "${it.cols}×${it.rows}" }
    parts += if (at.typing) "you may type" else "watching"
    return parts.joinToString(" · ")
}

/** What can be set about a terminal: how big its text is drawn, and whether the keyboard is up. */
@Composable
private fun TermSettings(typing: Boolean, view: TermView?, textSp: Float, keyboard: Boolean, sized: (Float) -> Unit) {
    TopicMenu { close ->
        Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(start = 16.dp, end = 4.dp)) {
            Text("Text size", modifier = Modifier.weight(1f, fill = false).padding(end = 12.dp))
            IconButton(onClick = { view?.let { it.stepText(larger = false); sized(it.textSp) } }) { Icon(Icons.Filled.Remove, "Smaller") }
            Text("%.0f".format(textSp), fontFamily = FontFamily.Monospace)
            IconButton(onClick = { view?.let { it.stepText(larger = true); sized(it.textSp) } }) { Icon(Icons.Filled.Add, "Larger") }
        }
        if (typing) {
            DropdownMenuItem(
                text = { Text(if (keyboard) "Hide keyboard" else "Show keyboard") },
                leadingIcon = { Icon(Icons.Filled.Keyboard, null) },
                onClick = {
                    close()
                    if (keyboard) view?.hideKeyboard() else view?.showKeyboard()
                },
            )
        }
    }
}

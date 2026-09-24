package dev.bresilla.drop.ui

import android.app.Activity
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material3.AssistChip
import androidx.compose.material3.AssistChipDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.TextUnit
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.view.WindowCompat
import dev.bresilla.drop.Drop
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch
import mobile.Live
import org.json.JSONObject

/** One stretch of a row that shares a style. */
private class Run(val text: String, val fg: Color?, val bg: Color?, val bold: Boolean, val dim: Boolean, val italic: Boolean, val under: Boolean)

/** A whole screen, as the Go side painted it. */
private class Frame(val cols: Int, val rows: Int, val lines: List<AnnotatedString>) {
    companion object {
        fun parse(text: String): Frame {
            val o = JSONObject(text)
            val cols = o.optInt("cols", 80)
            val rows = o.optInt("rows", 24)
            val lines = o.optJSONObject("lines")
            val drawn = List(rows) { y ->
                val runs = lines?.optJSONArray(y.toString())
                buildAnnotatedString {
                    if (runs != null) {
                        for (i in 0 until runs.length()) {
                            val r = runs.getJSONObject(i)
                            val run = Run(
                                r.optString("t"),
                                colour(r.optString("f")),
                                colour(r.optString("b")),
                                r.optBoolean("o"),
                                r.optBoolean("d"),
                                r.optBoolean("i"),
                                r.optBoolean("u"),
                            )
                            withStyle(
                                SpanStyle(
                                    color = (run.fg ?: Ink).let { if (run.dim) it.copy(alpha = 0.6f) else it },
                                    background = run.bg ?: Color.Unspecified,
                                    fontWeight = if (run.bold) FontWeight.Bold else null,
                                    fontStyle = if (run.italic) FontStyle.Italic else null,
                                    textDecoration = if (run.under) TextDecoration.Underline else null,
                                ),
                            ) { append(run.text) }
                        }
                    }
                }
            }
            return Frame(cols, rows, drawn)
        }
    }
}

private val Ink = Color(0xFFE6E6EB)
private val Ground = Color(0xFF0C0C10)

/** The first sixteen colours, as a terminal with a dark ground would draw them. */
private val Sixteen = listOf(
    0xFF1E1E24, 0xFFE5534B, 0xFF57AB5A, 0xFFC69026, 0xFF539BF5, 0xFFB083F0, 0xFF39C5CF, 0xFFD0D0D8,
    0xFF636E7B, 0xFFFF7B72, 0xFF6BC46D, 0xFFDAAA3F, 0xFF6CB6FF, 0xFFDCBDFB, 0xFF56D4DD, 0xFFFFFFFF,
).map { Color(it) }

private val rgb = Regex("""rgb\((\d+),(\d+),(\d+)\)""")
private val named = Regex("""var\(--t(\d+)\)""")

private fun colour(css: String): Color? {
    if (css.isEmpty()) return null
    rgb.matchEntire(css)?.let { m ->
        val (r, g, b) = m.destructured
        return Color(r.toInt(), g.toInt(), b.toInt())
    }
    named.matchEntire(css)?.let { m -> return Sixteen.getOrNull(m.groupValues[1].toInt()) }
    return null
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun LiveScreen(at: Screen.Live, back: () -> Unit) {
    val scope = rememberCoroutineScope()
    val frames = remember { MutableStateFlow<Frame?>(null) }
    val ends = remember { MutableStateFlow<String?>(null) }
    val companies = remember { MutableStateFlow<Pair<Long, Boolean>?>(null) }
    val frame by frames.collectAsState()
    LightBars()
    val ended by ends.collectAsState()
    val company by companies.collectAsState()
    var live by remember { mutableStateOf<Live?>(null) }

    DisposableEffect(at) {
        val screen = object : mobile.Screen {
            override fun drawn(frame: String) {
                frames.value = runCatching { Frame.parse(frame) }.getOrNull() ?: frames.value
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
            Drop.call { it.watch(at.machine, at.path, at.archetype, 80, 24, screen) }
                .onSuccess { started = it; live = it }
                .onFailure { ends.value = it.message ?: "could not open it" }
        }
        onDispose {
            job.cancel()
            started?.stop()
        }
    }

    Scaffold(
        containerColor = Ground,
        topBar = {
            TopAppBar(
                title = {
                    Column {
                        Text(at.path.trimStart('/'))
                        Text(
                            describe(at, company, frame),
                            style = Mono,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                },
                navigationIcon = { IconButton(onClick = back) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = Ground,
                    titleContentColor = Ink,
                    navigationIconContentColor = Ink,
                ),
            )
        },
        bottomBar = { if (at.typing) Keys(live) },
    ) { pad ->
        BoxWithConstraints(Modifier.fillMaxSize().padding(pad).background(Ground).padding(4.dp)) {
            // A terminal this phone is driving is sized for this phone, the way a window on a computer
            // sizes the one it shows. One that is only being watched keeps the size it was given.
            val readable = with(LocalDensity.current) { 11.sp.toDp() }
            val wantCols = (maxWidth / (readable * 0.6f)).toInt().coerceAtLeast(20)
            val wantRows = (maxHeight / (readable * 1.18f)).toInt().coerceAtLeast(8)
            if (at.typing) {
                LaunchedEffect(live, wantCols, wantRows) {
                    val talking = live ?: return@LaunchedEffect
                    Drop.call { talking.resize(wantCols.toLong(), wantRows.toLong()) }
                }
            }
            val f = frame
            when {
                f == null -> Text(ended ?: "connecting…", color = Ink, modifier = Modifier.padding(16.dp))
                else -> {
                    val cols = f.cols.coerceAtLeast(20)
                    // Sized so a whole row fits across, which is what makes a screen legible rather
                    // than a strip to scroll along; a monospace glyph is about 0.6 of its size wide.
                    val fit = with(LocalDensity.current) { (maxWidth / (cols * 0.6f)).toSp() }
                    val type: TextUnit = if (fit.value < 6f) 6.sp else fit
                    Column(Modifier.horizontalScroll(rememberScrollState())) {
                        for (line in f.lines) {
                            Text(
                                line,
                                color = Ink,
                                fontFamily = FontFamily.Monospace,
                                fontSize = type,
                                lineHeight = type * 1.18f,
                                softWrap = false,
                                maxLines = 1,
                            )
                        }
                        ended?.let { Text("— $it", color = Color(0xFFFF7B72), fontFamily = FontFamily.Monospace, fontSize = 12.sp) }
                    }
                }
            }
        }
    }
}

/** What a phone keyboard does not have, and a terminal needs. */
@Composable
private fun Keys(live: Live?) {
    val scope = rememberCoroutineScope()
    var line by remember { mutableStateOf("") }
    val send: (String) -> Unit = { text -> scope.launch { Drop.call { live?.type(text) } } }

    Surface(color = Color(0xFF17171D), modifier = Modifier.fillMaxWidth()) {
        Column(Modifier.navigationBarsPadding().imePadding().padding(8.dp)) {
            Row(
                horizontalArrangement = Arrangement.spacedBy(6.dp),
                modifier = Modifier.horizontalScroll(rememberScrollState()),
            ) {
                for ((label, bytes) in listOf(
                    "esc" to "\u001b", "tab" to "\t", "ctrl-c" to "\u0003", "ctrl-d" to "\u0004",
                    "↑" to "\u001b[A", "↓" to "\u001b[B", "←" to "\u001b[D", "→" to "\u001b[C", "enter" to "\r",
                )) {
                    AssistChip(
                        onClick = { send(bytes) },
                        label = { Text(label, fontFamily = FontFamily.Monospace) },
                        colors = AssistChipDefaults.assistChipColors(labelColor = Ink),
                        border = BorderStroke(1.dp, Color(0x44FFFFFF)),
                    )
                }
            }
            Row {
                OutlinedTextField(
                    value = line,
                    onValueChange = { line = it },
                    singleLine = true,
                    placeholder = { Text("type a line") },
                    textStyle = MaterialTheme.typography.bodyMedium.copy(fontFamily = FontFamily.Monospace, color = Ink),
                    shape = RoundedCornerShape(16.dp),
                    colors = OutlinedTextFieldDefaults.colors(
                        focusedTextColor = Ink,
                        unfocusedTextColor = Ink,
                        cursorColor = Ink,
                        unfocusedBorderColor = Color(0x44FFFFFF),
                        focusedPlaceholderColor = Color(0x88FFFFFF),
                        unfocusedPlaceholderColor = Color(0x88FFFFFF),
                    ),
                    modifier = Modifier.weight(1f),
                )
                IconButton(onClick = { send(line + "\r"); line = "" }) {
                    Icon(Icons.AutoMirrored.Filled.Send, "Type it", tint = Ink)
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
private fun describe(at: Screen.Live, company: Pair<Long, Boolean>?, frame: Frame?): String {
    val parts = mutableListOf(at.machine)
    company?.let { (watching, own) ->
        parts += when {
            own -> "your own shell"
            watching > 1 -> "shared, $watching watching"
            else -> "shared, only you"
        }
    }
    frame?.let { parts += "${it.cols}×${it.rows}" }
    parts += if (at.typing) "you may type" else "watching"
    return parts.joinToString(" · ")
}

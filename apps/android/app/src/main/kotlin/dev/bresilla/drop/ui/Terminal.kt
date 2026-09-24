package dev.bresilla.drop.ui

import android.content.Context
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.Typeface
import android.text.InputType
import android.view.GestureDetector
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.ScaleGestureDetector
import android.view.View
import android.view.inputmethod.BaseInputConnection
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.InputConnection
import android.view.inputmethod.InputMethodManager
import org.json.JSONObject
import kotlin.math.ceil
import kotlin.math.max
import kotlin.math.min

/** One stretch of a row that shares a style, from the column it starts at. Colours are ARGB. */
class Stretch(
    val col: Int,
    val text: String,
    val cells: Int,
    val fg: Int,
    val bg: Int?,
    val bold: Boolean,
    val dim: Boolean,
    val italic: Boolean,
    val under: Boolean,
)

/** A whole screen as the far end has it: its shape, its rows, and where the cursor stands. */
class Grid(val cols: Int, val rows: Int, val lines: List<List<Stretch>>, val cursorCol: Int, val cursorRow: Int) {
    companion object {
        const val INK = 0xFFE6E6EB.toInt()
        const val GROUND = 0xFF0C0C10.toInt()

        /** The first sixteen colours, as a terminal with a dark ground draws them. */
        private val Sixteen = intArrayOf(
            0xFF1E1E24.toInt(), 0xFFE5534B.toInt(), 0xFF57AB5A.toInt(), 0xFFC69026.toInt(),
            0xFF539BF5.toInt(), 0xFFB083F0.toInt(), 0xFF39C5CF.toInt(), 0xFFD0D0D8.toInt(),
            0xFF636E7B.toInt(), 0xFFFF7B72.toInt(), 0xFF6BC46D.toInt(), 0xFFDAAA3F.toInt(),
            0xFF6CB6FF.toInt(), 0xFFDCBDFB.toInt(), 0xFF56D4DD.toInt(), 0xFFFFFFFF.toInt(),
        )
        private val rgb = Regex("""rgb\((\d+),(\d+),(\d+)\)""")
        private val named = Regex("""var\(--t(\d+)\)""")

        /** A colour as the Go side names it; an inverted default pair names the ground and the ink. */
        private fun colour(css: String): Int? = when {
            css.isEmpty() -> null
            css == "var(--term-bg)" -> GROUND
            css == "var(--term-fg)" -> INK
            else -> rgb.matchEntire(css)?.destructured?.let { (r, g, b) ->
                (0xFF shl 24) or (r.toInt() shl 16) or (g.toInt() shl 8) or b.toInt()
            } ?: named.matchEntire(css)?.let { Sixteen.getOrNull(it.groupValues[1].toInt()) }
        }

        fun parse(text: String): Grid {
            val o = JSONObject(text)
            val cols = o.optInt("cols", 80)
            val rows = o.optInt("rows", 24)
            val lines = o.optJSONObject("lines")
            val drawn = List(rows) { y ->
                val runs = lines?.optJSONArray(y.toString()) ?: return@List emptyList()
                var col = 0
                List(runs.length()) { i ->
                    val r = runs.getJSONObject(i)
                    val t = r.optString("t")
                    // A cell is a rune on the Go side, which is a code point here and not a char.
                    val cells = t.codePointCount(0, t.length)
                    Stretch(
                        col, t, cells,
                        colour(r.optString("f")) ?: INK,
                        colour(r.optString("b")),
                        r.optBoolean("o"), r.optBoolean("d"), r.optBoolean("i"), r.optBoolean("u"),
                    ).also { col += cells }
                }
            }
            val cursor = o.optJSONArray("cursor")
            return Grid(cols, rows, drawn, cursor?.optInt(0, -1) ?: -1, cursor?.optInt(1, -1) ?: -1)
        }
    }
}

/**
 * A terminal, drawn cell by cell and typed into directly, the way Termux does it: the keyboard
 * writes into the terminal itself, with no line to type into first.
 *
 * Every cell is placed at its own column rather than left to the font to advance, because a
 * monospace font is monospace only for the glyphs it has — a box-drawing line from a fallback font
 * is some other width, and one of those per row is enough to push everything after it out of true.
 * The grid is as many cells as the view holds, which is what it tells the far end it is.
 */
class TermView(context: Context) : View(context) {
    var grid: Grid? = null
        set(value) {
            field = value
            invalidate()
        }

    /** Whether keys go anywhere. A terminal that is only watched takes none. */
    var typing = false
        set(value) {
            field = value
            isFocusable = value
            isFocusableInTouchMode = value
        }

    var onType: (String) -> Unit = {}
    var onSize: (cols: Int, rows: Int) -> Unit = { _, _ -> }
    var onModifiers: (ctrl: Boolean, alt: Boolean) -> Unit = { _, _ -> }
    var onTextSize: (Float) -> Unit = {}

    private var ctrl = false
    private var alt = false

    private val ink = Paint(Paint.ANTI_ALIAS_FLAG).apply { typeface = Typeface.MONOSPACE }
    private val fill = Paint()
    private var cellW = 1f
    private var cellH = 1f
    private var baseline = 0f
    private var told = 0 to 0

    /** How big a cell is drawn, in pixels of text size; pinching changes it, and the grid with it. */
    var textSize = 12f * resources.displayMetrics.scaledDensity
        set(value) {
            field = value.coerceIn(6f * resources.displayMetrics.scaledDensity, 32f * resources.displayMetrics.scaledDensity)
            measureCells()
            tellSize()
            invalidate()
        }

    init {
        measureCells()
        setBackgroundColor(Grid.GROUND)
    }

    private fun measureCells() {
        ink.textSize = textSize
        cellW = max(1f, ink.measureText("M"))
        val metrics = ink.fontMetrics
        cellH = max(1f, ceil(metrics.descent - metrics.ascent))
        baseline = -metrics.ascent
    }

    private fun tellSize() {
        if (width == 0 || height == 0) return
        val now = max(1, (width / cellW).toInt()) to max(1, (height / cellH).toInt())
        if (now != told) {
            told = now
            onSize(now.first, now.second)
        }
    }

    override fun onSizeChanged(w: Int, h: Int, oldw: Int, oldh: Int) {
        super.onSizeChanged(w, h, oldw, oldh)
        tellSize()
    }

    // Keys go to whatever holds focus, and a terminal that let it go — to a rotation, to the
    // keyboard closing — hands an Enter to the back button instead of the shell.
    override fun onAttachedToWindow() {
        super.onAttachedToWindow()
        if (typing) post { requestFocus() }
    }

    override fun onWindowFocusChanged(hasWindowFocus: Boolean) {
        super.onWindowFocusChanged(hasWindowFocus)
        if (typing && hasWindowFocus) requestFocus()
    }

    override fun onConfigurationChanged(newConfig: android.content.res.Configuration) {
        super.onConfigurationChanged(newConfig)
        if (typing) post { requestFocus() }
    }

    override fun onDraw(canvas: Canvas) {
        super.onDraw(canvas)
        val g = grid ?: return

        // A far end held to a size larger than this view — shared with a bigger window — is drawn
        // smaller to fit whole, rather than cut at the edge.
        val fit = min(1f, min(width / (g.cols * cellW), height / (g.rows * cellH)))
        canvas.save()
        canvas.scale(fit, fit)

        for (y in 0 until min(g.rows, g.lines.size)) {
            val top = y * cellH
            for (s in g.lines[y]) {
                s.bg?.let {
                    fill.color = it
                    canvas.drawRect(s.col * cellW, top, (s.col + s.cells) * cellW, top + cellH, fill)
                }
                ink.color = if (s.dim) (s.fg and 0x00FFFFFF) or (0x99 shl 24) else s.fg
                ink.isFakeBoldText = s.bold
                ink.textSkewX = if (s.italic) -0.2f else 0f
                ink.isUnderlineText = s.under
                var col = s.col
                var at = 0
                while (at < s.text.length) {
                    val size = Character.charCount(s.text.codePointAt(at))
                    if (s.text[at] != ' ') canvas.drawText(s.text, at, at + size, col * cellW, top + baseline, ink)
                    at += size
                    col++
                }
            }
        }

        if (typing && g.cursorCol >= 0 && g.cursorRow in 0 until g.rows) {
            fill.color = (Grid.INK and 0x00FFFFFF) or (0x88 shl 24)
            val x = g.cursorCol * cellW
            val top = g.cursorRow * cellH
            canvas.drawRect(x, top, x + cellW, top + cellH, fill)
        }
        canvas.restore()
    }

    private val pinch = ScaleGestureDetector(context, object : ScaleGestureDetector.SimpleOnScaleGestureListener() {
        override fun onScale(detector: ScaleGestureDetector): Boolean {
            textSize *= detector.scaleFactor
            onTextSize(textSize)
            return true
        }
    })

    private val taps = GestureDetector(context, object : GestureDetector.SimpleOnGestureListener() {
        override fun onSingleTapUp(e: MotionEvent): Boolean {
            showKeyboard()
            return true
        }
    })

    override fun onTouchEvent(event: MotionEvent): Boolean {
        pinch.onTouchEvent(event)
        if (!pinch.isInProgress) taps.onTouchEvent(event)
        return true
    }

    fun showKeyboard() {
        if (!typing) return
        requestFocus()
        context.getSystemService(InputMethodManager::class.java)?.showSoftInput(this, InputMethodManager.SHOW_IMPLICIT)
    }

    fun toggleKeyboard() {
        if (!typing) return
        requestFocus()
        context.getSystemService(InputMethodManager::class.java)?.toggleSoftInput(0, 0)
    }

    /** CTRL or ALT from the row of extra keys, held for the next key and then let go. */
    fun toggle(control: Boolean) {
        if (control) ctrl = !ctrl else alt = !alt
        onModifiers(ctrl, alt)
    }

    /** Text typed: a held CTRL makes a control character of it, and a held ALT puts ESC first. */
    fun type(text: String) {
        if (text.isEmpty()) return
        var out = text.replace("\n", "\r")
        if (ctrl && out.length == 1) out = controlOf(out[0])
        if (alt) out = "\u001b" + out
        release()
        onType(out)
    }

    /** A key that is a sequence of its own — an arrow, ESC, a page — which CTRL does not change. */
    fun key(sequence: String) {
        val out = if (alt) "\u001b" + sequence else sequence
        release()
        onType(out)
    }

    private fun release() {
        if (ctrl || alt) {
            ctrl = false
            alt = false
            onModifiers(false, false)
        }
    }

    private fun controlOf(c: Char): String = when (c) {
        in 'a'..'z' -> (c - 'a' + 1).toChar().toString()
        in 'A'..'Z' -> (c - 'A' + 1).toChar().toString()
        ' ', '@', '2' -> "\u0000"
        '[', '3' -> "\u001b"
        '\\', '4' -> "\u001c"
        ']', '5' -> "\u001d"
        '^', '6' -> "\u001e"
        '_', '-', '7' -> "\u001f"
        '?', '8' -> "\u007f"
        else -> c.toString()
    }

    override fun onCheckIsTextEditor(): Boolean = typing

    override fun onCreateInputConnection(outAttrs: EditorInfo): InputConnection? {
        if (!typing) return null
        // As a password nobody can see: no suggestions, no autocorrect, and keys arrive one at a
        // time, which is what a terminal wants — a word held back until a space is a command that
        // runs late.
        outAttrs.inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_VISIBLE_PASSWORD or
            InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
        outAttrs.imeOptions = EditorInfo.IME_FLAG_NO_FULLSCREEN or EditorInfo.IME_FLAG_NO_EXTRACT_UI or EditorInfo.IME_ACTION_NONE
        return object : BaseInputConnection(this, true) {
            override fun commitText(text: CharSequence, newCursorPosition: Int): Boolean {
                super.commitText(text, newCursorPosition)
                flush()
                return true
            }

            override fun finishComposingText(): Boolean {
                super.finishComposingText()
                flush()
                return true
            }

            override fun deleteSurroundingText(beforeLength: Int, afterLength: Int): Boolean {
                repeat(beforeLength) { type("\u007f") }
                return true
            }

            private fun flush() {
                val content = editable ?: return
                val text = content.toString()
                content.clear()
                type(text)
            }
        }
    }

    override fun onKeyDown(keyCode: Int, event: KeyEvent): Boolean {
        if (!typing) return super.onKeyDown(keyCode, event)
        val sequence = when (keyCode) {
            KeyEvent.KEYCODE_ENTER, KeyEvent.KEYCODE_NUMPAD_ENTER -> "\r"
            KeyEvent.KEYCODE_DEL -> "\u007f"
            KeyEvent.KEYCODE_FORWARD_DEL -> "\u001b[3~"
            KeyEvent.KEYCODE_TAB -> "\t"
            KeyEvent.KEYCODE_ESCAPE -> "\u001b"
            KeyEvent.KEYCODE_DPAD_UP -> "\u001b[A"
            KeyEvent.KEYCODE_DPAD_DOWN -> "\u001b[B"
            KeyEvent.KEYCODE_DPAD_RIGHT -> "\u001b[C"
            KeyEvent.KEYCODE_DPAD_LEFT -> "\u001b[D"
            KeyEvent.KEYCODE_MOVE_HOME -> "\u001b[H"
            KeyEvent.KEYCODE_MOVE_END -> "\u001b[F"
            KeyEvent.KEYCODE_PAGE_UP -> "\u001b[5~"
            KeyEvent.KEYCODE_PAGE_DOWN -> "\u001b[6~"
            else -> null
        }
        if (sequence != null) {
            if (keyCode == KeyEvent.KEYCODE_ENTER || keyCode == KeyEvent.KEYCODE_DEL || keyCode == KeyEvent.KEYCODE_TAB) type(sequence) else key(sequence)
            return true
        }
        // A key a hardware keyboard sends: its character, with its own CTRL and ALT counted too.
        val c = event.getUnicodeChar(event.metaState and (KeyEvent.META_CTRL_MASK or KeyEvent.META_ALT_MASK).inv())
        if (c == 0) return super.onKeyDown(keyCode, event)
        if (event.isCtrlPressed && !ctrl) ctrl = true
        if (event.isAltPressed && !alt) alt = true
        type(String(Character.toChars(c)))
        return true
    }
}

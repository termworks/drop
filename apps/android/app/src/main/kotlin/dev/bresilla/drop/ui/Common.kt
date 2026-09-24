package dev.bresilla.drop.ui

import android.content.ActivityNotFoundException
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.media.MediaScannerConnection
import android.net.Uri
import android.provider.OpenableColumns
import android.text.format.DateUtils
import android.text.format.Formatter
import android.webkit.MimeTypeMap
import android.widget.Toast
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Chat
import androidx.compose.material.icons.filled.EditNote
import androidx.compose.material.icons.filled.Extension
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.Link
import androidx.compose.material.icons.filled.MoveToInbox
import androidx.compose.material.icons.filled.Sensors
import androidx.compose.material.icons.filled.Terminal
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.content.FileProvider
import dev.bresilla.drop.Drop
import java.io.File
import kotlin.math.absoluteValue
import kotlin.math.min

private val Hues = listOf(
    Color(0xFF5B4FD6), Color(0xFF2F6FDB), Color(0xFF00897B), Color(0xFFD84F7A),
    Color(0xFFE07A1F), Color(0xFF7B5CC4), Color(0xFF3A8F3E), Color(0xFFB0453A),
)

/** One colour per name, the same one every time, so somebody is recognisable at a glance. */
fun hue(name: String): Color = Hues[name.fold(7) { h, c -> h * 131 + c.code }.absoluteValue % Hues.size]

@Composable
fun Avatar(name: String, size: Dp = 44.dp, online: Boolean? = null) {
    Box {
        Box(
            modifier = Modifier.size(size).clip(CircleShape).background(hue(name)),
            contentAlignment = Alignment.Center,
        ) {
            Text(
                text = name.take(1).uppercase().ifEmpty { "?" },
                color = Color.White,
                fontWeight = FontWeight.SemiBold,
                fontSize = (size.value * 0.42f).sp,
            )
        }
        if (online != null) {
            Box(
                modifier = Modifier
                    .align(Alignment.BottomEnd)
                    .size(size * 0.3f)
                    .clip(CircleShape)
                    .background(MaterialTheme.colorScheme.surface)
                    .padding(2.dp)
                    .clip(CircleShape)
                    .background(if (online) Color(0xFF2DB55D) else MaterialTheme.colorScheme.outlineVariant),
            )
        }
    }
}

/** What a kind of path looks like, and the colour that tells it apart from the others. */
data class Look(val icon: ImageVector, val tint: Color, val verb: String)

fun look(kind: String): Look = when (kind) {
    "chat" -> Look(Icons.AutoMirrored.Filled.Chat, Color(0xFF5B4FD6), "Talk")
    "files" -> Look(Icons.Filled.Folder, Color(0xFF2F6FDB), "Browse")
    "share" -> Look(Icons.Filled.MoveToInbox, Color(0xFF00897B), "Send files")
    "link" -> Look(Icons.Filled.Link, Color(0xFFE07A1F), "Send a link")
    "note" -> Look(Icons.Filled.EditNote, Color(0xFFD84F7A), "Shared note")
    "stream" -> Look(Icons.Filled.Sensors, Color(0xFF3A8F3E), "Watch")
    "tty" -> Look(Icons.Filled.Terminal, Color(0xFF263238), "Open terminal")
    else -> Look(Icons.Filled.Extension, Color(0xFF7B5CC4), "Open")
}

@Composable
fun KindBadge(kind: String, size: Dp = 44.dp) {
    val it = look(kind)
    Box(
        modifier = Modifier.size(size).clip(RoundedCornerShape(size * 0.3f)).background(it.tint.copy(alpha = 0.14f)),
        contentAlignment = Alignment.Center,
    ) {
        Icon(it.icon, contentDescription = kind, tint = it.tint, modifier = Modifier.size(size * 0.55f))
    }
}

@Composable
fun Section(title: String, modifier: Modifier = Modifier) {
    Text(
        text = title.uppercase(),
        style = MaterialTheme.typography.labelMedium,
        color = MaterialTheme.colorScheme.primary,
        letterSpacing = 1.2.sp,
        modifier = modifier.padding(start = 20.dp, end = 20.dp, top = 20.dp, bottom = 8.dp),
    )
}

/** A card saying what went wrong, in the list where it went wrong. */
@Composable
fun Banner(text: String, modifier: Modifier = Modifier, error: Boolean = false) {
    Surface(
        color = if (error) MaterialTheme.colorScheme.errorContainer else MaterialTheme.colorScheme.secondaryContainer,
        contentColor = if (error) MaterialTheme.colorScheme.onErrorContainer else MaterialTheme.colorScheme.onSecondaryContainer,
        shape = RoundedCornerShape(16.dp),
        modifier = modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 6.dp),
    ) {
        Text(text, style = MaterialTheme.typography.bodyMedium, modifier = Modifier.padding(14.dp))
    }
}

/** A transfer under way, drawn wherever the screen has room for it. */
@Composable
fun Transfer(modifier: Modifier = Modifier) {
    val moving by Drop.moving.collectAsState()
    val at = moving ?: return
    Column(modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp)) {
        Row(horizontalArrangement = Arrangement.SpaceBetween, modifier = Modifier.fillMaxWidth()) {
            Text(at.name, style = MaterialTheme.typography.labelLarge, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
            Text("${(at.fraction * 100).toInt()}%", style = MaterialTheme.typography.labelLarge)
        }
        Spacer(Modifier.height(6.dp))
        LinearProgressIndicator(progress = { at.fraction }, modifier = Modifier.fillMaxWidth().clip(CircleShape))
    }
}

fun size(context: Context, bytes: Long): String = Formatter.formatShortFileSize(context, bytes)

fun ago(at: Long): String {
    // Two clocks never quite agree, and something written a second ago over there is not "in a minute".
    val now = System.currentTimeMillis()
    return DateUtils.getRelativeTimeSpanString(min(at, now), now, DateUtils.MINUTE_IN_MILLIS, DateUtils.FORMAT_ABBREV_RELATIVE).toString()
}

fun copy(context: Context, label: String, text: String) {
    context.getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText(label, text))
    Toast.makeText(context, "Copied", Toast.LENGTH_SHORT).show()
}

fun share(context: Context, text: String) {
    val send = Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, text)
    context.startActivity(Intent.createChooser(send, null))
}

fun browse(context: Context, link: String) {
    try {
        context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(link)))
    } catch (_: ActivityNotFoundException) {
        Toast.makeText(context, "Nothing here opens that", Toast.LENGTH_SHORT).show()
    }
}

/** Opens something that arrived in whichever app handles its kind of file. */
fun open(context: Context, file: File) {
    MediaScannerConnection.scanFile(context, arrayOf(file.absolutePath), null, null)
    val uri = FileProvider.getUriForFile(context, "dev.bresilla.drop.files", file)
    val type = MimeTypeMap.getSingleton().getMimeTypeFromExtension(file.extension.lowercase()) ?: "*/*"
    val view = Intent(Intent.ACTION_VIEW).setDataAndType(uri, type).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
    try {
        context.startActivity(Intent.createChooser(view, file.name))
    } catch (_: ActivityNotFoundException) {
        Toast.makeText(context, "Nothing here opens ${file.name}", Toast.LENGTH_SHORT).show()
    }
}

/** Where a file that arrived by the name it was sent under is, if it is still there. */
fun landed(name: String): File? {
    if (!Drop.isReady()) return null
    val at = File(File(Drop.downloads, "drop"), File(name).name)
    return at.takeIf { it.isFile }
}

/**
 * Copies what another app handed over into somewhere Go can read it by path: a content URI is an
 * Android idea, and the node on the other side of the binding only knows files.
 */
fun stage(context: Context, uris: List<Uri>): List<File> {
    val into = File(context.cacheDir, "outgoing").apply { mkdirs() }
    return uris.mapNotNull { uri ->
        val name = displayName(context, uri) ?: uri.lastPathSegment ?: "file"
        val out = File(into, File(name).name)
        runCatching {
            context.contentResolver.openInputStream(uri)?.use { input -> out.outputStream().use { input.copyTo(it) } }
            out
        }.getOrNull()
    }
}

private fun displayName(context: Context, uri: Uri): String? =
    context.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { cursor ->
        if (cursor.moveToFirst()) cursor.getString(0) else null
    }

/** Who is reachable changes without anything arriving, so a screen that shows it looks again now and then. */
@Composable
fun Pulse(every: Long = 5_000) {
    androidx.compose.runtime.LaunchedEffect(Unit) {
        while (true) {
            kotlinx.coroutines.delay(every)
            Drop.bump()
        }
    }
}

package dev.bresilla.drop.ui

import android.graphics.BitmapFactory
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.ContentCopy
import androidx.compose.material.icons.filled.QrCodeScanner
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import dev.bresilla.drop.Drop
import dev.bresilla.drop.Me
import dev.bresilla.drop.News
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import mobile.Mobile

/**
 * Adding a machine of yours is one code: a computer shows it with `drop machine add` and this phone
 * scans it, or this phone shows one and the computer types it. Either way the other becomes yours.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AddMachineScreen(given: String?, back: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var me by remember { mutableStateOf<Me?>(null) }
    var taking by remember { mutableStateOf(false) }
    var told by remember { mutableStateOf<String?>(null) }
    var failed by remember { mutableStateOf<String?>(null) }
    var typing by remember { mutableStateOf(false) }
    var showing by remember { mutableStateOf(false) }

    LaunchedEffect(Unit) { me = Drop.self() }
    LaunchedEffect(Unit) {
        Drop.news.collect { if (it is News.Paired) { showing = false; told = "${it.with} is one of your machines now." } }
    }

    val take: (String) -> Unit = { code ->
        taking = true
        failed = null
        told = null
        scope.launch {
            Drop.joinMachine(context.applicationContext, code)
                .onSuccess { told = it }
                .onFailure { failed = it.message }
            me = Drop.self()
            taking = false
        }
    }
    LaunchedEffect(given) { if (given != null) take(given) }

    val camera = rememberLauncherForActivityResult(ScanContract()) { result -> result.contents?.let(take) }
    val scan = {
        camera.launch(
            ScanOptions()
                .setDesiredBarcodeFormats(ScanOptions.QR_CODE)
                .setPrompt("Point at the code drop machine add is showing")
                .setBeepEnabled(false)
                .setOrientationLocked(false),
        )
    }

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
                    title = "Join a computer of yours",
                    says = "On a computer of yours, run",
                    command = "drop machine add",
                    after = "then scan the code it shows, or type it.",
                ) {
                    Row(horizontalArrangement = Arrangement.spacedBy(10.dp), verticalAlignment = Alignment.CenterVertically) {
                        Button(onClick = scan, enabled = !taking) {
                            Icon(Icons.Filled.QrCodeScanner, null, Modifier.size(18.dp))
                            Spacer(Modifier.width(6.dp))
                            Text("Scan")
                        }
                        TextButton(onClick = { typing = true }, enabled = !taking) { Text("Type the code") }
                        if (taking) CircularProgressIndicator(Modifier.size(22.dp), strokeWidth = 2.dp)
                    }
                    told?.let { Banner(it, Modifier.padding(top = 8.dp)) }
                    failed?.let { Banner(it, Modifier.padding(top = 8.dp), error = true) }
                }
            }
            if (me?.signs == true) {
                item {
                    WayIn(
                        title = "Add a computer to this phone",
                        says = "Show a code here, and on the computer run",
                        command = "drop add <code>",
                        after = "It becomes one of your machines, signed for by this phone.",
                    ) {
                        if (showing) MachineCode() else FilledTonalButton(onClick = { showing = true }) { Text("Show a code") }
                    }
                }
            }
        }
    }

    if (typing) {
        var typed by remember { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { typing = false },
            title = { Text("Type the code") },
            text = { OutlinedTextField(typed, { typed = it }, placeholder = { Text("abcd-efgh-ijkl") }, textStyle = Mono, singleLine = true) },
            confirmButton = {
                TextButton(enabled = typed.isNotBlank(), onClick = { typing = false; take(typed) }) { Text("Join") }
            },
            dismissButton = { TextButton(onClick = { typing = false }) { Text("Cancel") } },
        )
    }
}

/** This phone's code for a computer to join with, up for as long as it is on screen. */
@Composable
internal fun MachineCode() {
    val context = LocalContext.current
    var code by remember { mutableStateOf<String?>(null) }
    var drawn by remember { mutableStateOf<ImageBitmap?>(null) }
    var failed by remember { mutableStateOf<String?>(null) }

    LaunchedEffect(Unit) {
        Drop.call { it.offerMachine() }
            .onSuccess { ticket ->
                code = ticket.substringAfter('#')
                drawn = withContext(Dispatchers.Default) {
                    val png = Mobile.machineCode(ticket, 12)
                    BitmapFactory.decodeByteArray(png, 0, png.size)?.asImageBitmap()
                }
            }
            .onFailure { failed = it.message }
    }
    DisposableEffect(Unit) { onDispose { Drop.node?.stopOffer() } }

    Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = Modifier.fillMaxWidth()) {
        failed?.let { Banner(it, error = true) }
        code?.let { c ->
            Text(c, style = MaterialTheme.typography.headlineSmall.copy(fontFamily = Mono.fontFamily), color = MaterialTheme.colorScheme.primary)
            Spacer(Modifier.height(8.dp))
            OutlinedButton(onClick = { copy(context, "drop machine code", "drop add $c") }) {
                Icon(Icons.Filled.ContentCopy, null, Modifier.size(18.dp))
                Spacer(Modifier.width(8.dp))
                Text("Copy the command")
            }
            Spacer(Modifier.height(12.dp))
        }
        Surface(color = Color.White, shape = RoundedCornerShape(20.dp), modifier = Modifier.fillMaxWidth(0.7f).aspectRatio(1f)) {
            Box(contentAlignment = Alignment.Center) {
                val image = drawn
                if (image == null) {
                    if (failed == null) CircularProgressIndicator()
                } else {
                    Image(image, "machine code", filterQuality = FilterQuality.None, modifier = Modifier.fillMaxSize().padding(10.dp))
                }
            }
        }
        Spacer(Modifier.height(8.dp))
        Text(
            "Works once, for ten minutes, while this is open.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

/** One way of adding a machine: what it does, the command it takes, and what to press here. */
@Composable
private fun WayIn(title: String, says: String, command: String, after: String, actions: @Composable () -> Unit) {
    Card(
        modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp).fillMaxWidth(),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
        shape = RoundedCornerShape(24.dp),
    ) {
        Column(Modifier.padding(20.dp)) {
            Text(title, style = MaterialTheme.typography.titleLarge)
            Spacer(Modifier.height(8.dp))
            Text(says, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(Modifier.height(6.dp))
            Surface(color = MaterialTheme.colorScheme.surfaceContainerHigh, shape = RoundedCornerShape(12.dp), modifier = Modifier.fillMaxWidth()) {
                Text(command, style = Mono, color = MaterialTheme.colorScheme.primary, modifier = Modifier.padding(12.dp))
            }
            Spacer(Modifier.height(6.dp))
            Text(after, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(Modifier.height(14.dp))
            actions()
        }
    }
}

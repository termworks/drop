package dev.bresilla.drop.ui

import android.graphics.BitmapFactory
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.ContentCopy
import androidx.compose.material.icons.filled.QrCodeScanner
import androidx.compose.material.icons.filled.Share
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Tab
import androidx.compose.material3.TabRow
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import androidx.activity.compose.rememberLauncherForActivityResult
import dev.bresilla.drop.Drop
import dev.bresilla.drop.News
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import mobile.Mobile

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PairScreen(ticket: String?, scan: Boolean, back: () -> Unit, paired: (String) -> Unit) {
    var tab by remember { mutableIntStateOf(if (scan || ticket != null) 1 else 0) }

    // Whichever way it happened, a pairing that completes goes straight to whoever it was with.
    LaunchedEffect(Unit) {
        Drop.news.collect { if (it is News.Paired) paired(it.with) }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Pair") },
                navigationIcon = { IconButton(onClick = back) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
            )
        },
    ) { pad ->
        Column(Modifier.fillMaxSize().padding(pad)) {
            TabRow(selectedTabIndex = tab) {
                Tab(selected = tab == 0, onClick = { tab = 0 }, text = { Text("My code") })
                Tab(selected = tab == 1, onClick = { tab = 1 }, text = { Text("Scan a code") })
            }
            when (tab) {
                0 -> ShowCode()
                else -> TakeCode(ticket, paired)
            }
        }
    }
}

/** This device's code, up for as long as it is on screen and no longer. */
@Composable
private fun ShowCode() {
    val context = LocalContext.current
    var ticket by remember { mutableStateOf<String?>(null) }
    var drawn by remember { mutableStateOf<ImageBitmap?>(null) }
    var failed by remember { mutableStateOf<String?>(null) }

    LaunchedEffect(Unit) {
        Drop.call { it.offer() }
            .onSuccess { offered ->
                ticket = offered
                drawn = withContext(Dispatchers.Default) {
                    val png = Mobile.code(offered, 12)
                    BitmapFactory.decodeByteArray(png, 0, png.size)?.asImageBitmap()
                }
            }
            .onFailure { failed = it.message }
    }
    DisposableEffect(Unit) { onDispose { Drop.node?.stopOffer() } }

    Column(
        Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(
            "Point the other phone's camera here — in drop, Scan a code.",
            style = MaterialTheme.typography.bodyLarge,
            textAlign = TextAlign.Center,
        )
        Spacer(Modifier.height(20.dp))
        Surface(
            color = Color.White,
            shape = RoundedCornerShape(28.dp),
            shadowElevation = 6.dp,
            modifier = Modifier.fillMaxWidth(0.85f).aspectRatio(1f),
        ) {
            Box(contentAlignment = Alignment.Center) {
                val image = drawn
                when {
                    failed != null -> Text(failed ?: "", color = Color.Black, modifier = Modifier.padding(24.dp))
                    image == null -> CircularProgressIndicator()
                    else -> Image(
                        bitmap = image,
                        contentDescription = "pairing code",
                        filterQuality = FilterQuality.None,
                        modifier = Modifier.fillMaxSize().padding(14.dp),
                    )
                }
            }
        }
        Spacer(Modifier.height(20.dp))
        Row(verticalAlignment = Alignment.CenterVertically) {
            CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
            Spacer(Modifier.width(10.dp))
            Text("Waiting for the other device", style = MaterialTheme.typography.labelLarge)
        }
        ticket?.let { t ->
            Spacer(Modifier.height(28.dp))
            Text("On a computer", style = MaterialTheme.typography.titleSmall)
            Spacer(Modifier.height(6.dp))
            Surface(
                color = MaterialTheme.colorScheme.surfaceContainerHigh,
                shape = RoundedCornerShape(14.dp),
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text("drop peer pair $t", style = Mono, modifier = Modifier.padding(12.dp))
            }
            Spacer(Modifier.height(10.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                OutlinedButton(onClick = { copy(context, "drop ticket", t) }) {
                    Icon(Icons.Filled.ContentCopy, null, Modifier.size(18.dp))
                    Spacer(Modifier.width(8.dp))
                    Text("Copy")
                }
                OutlinedButton(onClick = { share(context, "drop peer pair $t") }) {
                    Icon(Icons.Filled.Share, null, Modifier.size(18.dp))
                    Spacer(Modifier.width(8.dp))
                    Text("Share")
                }
            }
            Spacer(Modifier.height(12.dp))
            Text(
                "The code works once, for ten minutes, and only while this screen is open.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center,
            )
        }
    }
}

/** Somebody else's code: read off their screen, or pasted. */
@Composable
private fun TakeCode(given: String?, paired: (String) -> Unit) {
    val scope = rememberCoroutineScope()
    var text by remember { mutableStateOf(given ?: "") }
    var joining by remember { mutableStateOf(false) }
    var failed by remember { mutableStateOf<String?>(null) }
    var took by remember { mutableStateOf<String?>(null) }
    val context = LocalContext.current

    // A code from drop me user vouch or export makes this phone somebody's instead of pairing it.
    val join: (String) -> Unit = { ticket ->
        joining = true
        failed = null
        took = null
        scope.launch {
            if (Drop.owning(ticket)) {
                Drop.take(context.applicationContext, ticket)
                    .onSuccess { took = it }
                    .onFailure { failed = it.message }
            } else {
                Drop.call { it.join(ticket.trim()) }
                    .onSuccess { paired(it) }
                    .onFailure { failed = it.message }
            }
            joining = false
        }
    }

    val camera = rememberLauncherForActivityResult(ScanContract()) { result ->
        result.contents?.let { read ->
            text = read
            join(read)
        }
    }
    val look = {
        camera.launch(
            ScanOptions()
                .setDesiredBarcodeFormats(ScanOptions.QR_CODE)
                .setPrompt("Point at the code drop is showing")
                .setBeepEnabled(false)
                .setOrientationLocked(false),
        )
    }

    // A code that came from a tapped link is as good as one read off a screen.
    LaunchedEffect(given) { if (given != null) join(given) }

    Column(
        Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Box(
            Modifier.size(120.dp).clip(RoundedCornerShape(36.dp)).background(MaterialTheme.colorScheme.primaryContainer),
            contentAlignment = Alignment.Center,
        ) {
            Icon(Icons.Filled.QrCodeScanner, null, tint = MaterialTheme.colorScheme.onPrimaryContainer, modifier = Modifier.size(64.dp))
        }
        Spacer(Modifier.height(20.dp))
        Text(
            "On the computer run drop peer pair, or open My code on the other phone, then scan what it shows.",
            style = MaterialTheme.typography.bodyLarge,
            textAlign = TextAlign.Center,
        )
        Spacer(Modifier.height(20.dp))
        Button(onClick = look, enabled = !joining, modifier = Modifier.fillMaxWidth().height(52.dp)) {
            Icon(Icons.Filled.QrCodeScanner, null)
            Spacer(Modifier.width(10.dp))
            Text("Open the camera")
        }
        Spacer(Modifier.height(28.dp))
        Text("or paste the ticket", style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.height(8.dp))
        OutlinedTextField(
            value = text,
            onValueChange = { text = it },
            placeholder = { Text("9363f77d…#qxwo-e62y") },
            textStyle = Mono,
            modifier = Modifier.fillMaxWidth(),
        )
        Spacer(Modifier.height(12.dp))
        OutlinedButton(onClick = { join(text) }, enabled = !joining && (text.contains('#') || Drop.owning(text)), modifier = Modifier.fillMaxWidth()) {
            Text("Pair")
        }
        Spacer(Modifier.height(20.dp))
        when {
            joining -> Row(verticalAlignment = Alignment.CenterVertically) {
                CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                Spacer(Modifier.width(10.dp))
                Text("Reaching the other device…")
            }
            failed != null -> Banner(failed ?: "", error = true)
            took != null -> Banner(took ?: "")
        }
    }
}

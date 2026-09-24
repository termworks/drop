package dev.bresilla.drop.ui

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Key
import androidx.compose.material.icons.filled.QrCodeScanner
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import dev.bresilla.drop.Drop
import dev.bresilla.drop.Invited
import dev.bresilla.drop.Me
import dev.bresilla.drop.Near
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import com.yubico.yubikit.android.YubiKitManager
import com.yubico.yubikit.android.transport.nfc.NfcConfiguration
import com.yubico.yubikit.android.transport.usb.UsbConfiguration
import mobile.Mobile

/**
 * One way in for any code: a computer of yours adding this phone shows one with `drop machine add`,
 * anybody pairing shows one with `drop peer pair`, and the code itself says which it is.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun JoinScreen(given: String?, back: () -> Unit, done: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var typed by remember { mutableStateOf("") }
    var joining by remember { mutableStateOf(false) }
    var told by remember { mutableStateOf<String?>(null) }
    var failed by remember { mutableStateOf<String?>(null) }

    val join: (String) -> Unit = { code ->
        joining = true
        failed = null
        told = null
        scope.launch {
            Drop.joinAny(context.applicationContext, code)
                .onSuccess { told = it; Drop.bump() }
                .onFailure { failed = it.message }
            joining = false
        }
    }
    val camera = rememberLauncherForActivityResult(ScanContract()) { result -> result.contents?.let(join) }
    val scan = {
        camera.launch(
            ScanOptions()
                .setDesiredBarcodeFormats(ScanOptions.QR_CODE)
                .setPrompt("Point at the code drop is showing")
                .setBeepEnabled(false)
                .setOrientationLocked(false),
        )
    }
    LaunchedEffect(given) { if (given != null) join(given) }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Join") },
                navigationIcon = { IconButton(onClick = back) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
            )
        },
    ) { pad ->
        Column(
            Modifier.fillMaxSize().padding(pad).verticalScroll(rememberScrollState()).padding(24.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text(
                "Scan or type the code the other device shows. On a computer of yours that is drop machine add; on somebody else's, drop peer pair. The code says which.",
                style = MaterialTheme.typography.bodyLarge,
                textAlign = TextAlign.Center,
            )
            Spacer(Modifier.height(24.dp))
            Button(onClick = scan, enabled = !joining, modifier = Modifier.fillMaxWidth().height(56.dp)) {
                Icon(Icons.Filled.QrCodeScanner, null)
                Spacer(Modifier.width(10.dp))
                Text("Scan the code")
            }
            Spacer(Modifier.height(24.dp))
            Text("or type it", style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(Modifier.height(8.dp))
            OutlinedTextField(
                typed,
                { typed = it },
                placeholder = { Text("abcd-efgh-ijkl") },
                singleLine = true,
                textStyle = Mono,
                modifier = Modifier.fillMaxWidth(),
            )
            Spacer(Modifier.height(12.dp))
            FilledTonalButton(onClick = { join(typed) }, enabled = !joining && typed.isNotBlank(), modifier = Modifier.fillMaxWidth()) {
                Text("Join")
            }
            Spacer(Modifier.height(20.dp))
            if (joining) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                    Spacer(Modifier.width(10.dp))
                    Text("Reaching the other device…")
                }
            }
            failed?.let { Banner(it, error = true) }
            told?.let {
                Banner(it)
                Spacer(Modifier.height(12.dp))
                Button(onClick = done) { Text("Done") }
            }
        }
    }
}

/** A device asking this phone to connect, wherever the app happens to be. */
@Composable
fun InvitePrompt() {
    val scope = rememberCoroutineScope()
    var asking by remember { mutableStateOf<List<Invited>>(emptyList()) }
    LaunchedEffect(Unit) {
        while (true) {
            asking = Drop.invited()
            delay(1500)
        }
    }
    val one = asking.firstOrNull() ?: return
    val whose = if (one.whose.isNotEmpty() && one.whose != "you") " (${one.whose}'s)" else ""
    AlertDialog(
        onDismissRequest = {},
        title = { Text("${one.name}$whose wants to connect") },
        text = {
            Column {
                Text(
                    when (one.kind) {
                        "mine" -> "It asks to make this phone one of its owner's machines. This phone would be theirs from then on."
                        "join" -> "It asks to become one of your machines."
                        else -> "It asks to pair with you."
                    },
                )
                Spacer(Modifier.height(12.dp))
                Text("Say yes only if its screen shows the same number:")
                Spacer(Modifier.height(8.dp))
                Text(one.check, fontSize = 32.sp, fontWeight = FontWeight.Bold, color = MaterialTheme.colorScheme.primary)
            }
        },
        confirmButton = {
            Button(onClick = { scope.launch { Drop.decide(one.id, true); asking = asking.drop(1); delay(3000); Drop.bump() } }) { Text("Yes") }
        },
        dismissButton = {
            TextButton(onClick = { scope.launch { Drop.decide(one.id, false); asking = asking.drop(1) } }) { Text("No") }
        },
    )
}

/**
 * Adding a device, whoever's it is: one on this network by tapping it, any other by one of you
 * showing a code and the other taking it. What it is to you comes afterwards, on its own screen.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AddScreen(back: () -> Unit, added: (String) -> Unit) {
    val scope = rememberCoroutineScope()
    var which by remember { mutableIntStateOf(0) }
    var near by remember { mutableStateOf<List<Near>>(emptyList()) }
    var asking by remember { mutableStateOf<Near?>(null) }
    var said by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(Unit) {
        while (true) {
            near = Drop.nearby()
            delay(2000)
        }
    }
    LaunchedEffect(Unit) { Drop.news.collect { if (it is dev.bresilla.drop.News.Paired) added(it.with) } }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Add") },
                navigationIcon = { IconButton(onClick = back) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
            )
        },
    ) { pad ->
        // Only what is under the switch scrolls: the code it shows scrolls itself.
        Column(Modifier.fillMaxSize().padding(pad)) {
            if (near.isNotEmpty()) {
                Section("Nearby — tap to add")
                near.take(4).forEach { n -> Entry(n.name, "on this network", online = true, trusted = false) { asking = n } }
            }
            said?.let { Banner(it, Modifier.padding(horizontal = 16.dp)) }
            SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth().padding(horizontal = 24.dp, vertical = 12.dp)) {
                listOf("My code", "Their code").forEachIndexed { i, label ->
                    SegmentedButton(selected = which == i, onClick = { which = i }, shape = SegmentedButtonDefaults.itemShape(i, 2)) { Text(label) }
                }
            }
            Column(Modifier.weight(1f)) {
                if (which == 0) {
                    ShowCode()
                } else {
                    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState())) { TakeTheirs(done = { said = it }) }
                }
            }
        }
    }

    asking?.let { n ->
        AskDialog(
            id = n.id,
            name = n.name,
            kind = "pair",
            title = "Add ${n.name}?",
            says = "It is added as a device of its own. Afterwards you can make it one of your machines from its screen.",
            dismiss = { asking = null },
        ) { outcome -> said = outcome; scope.launch { Drop.bump() } }
    }
}

/** Scanning or typing the code another device shows. */
@Composable
private fun TakeTheirs(done: (String) -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var typed by remember { mutableStateOf("") }
    var joining by remember { mutableStateOf(false) }
    var failed by remember { mutableStateOf<String?>(null) }
    val take: (String) -> Unit = { code ->
        joining = true
        failed = null
        scope.launch {
            Drop.joinAny(context.applicationContext, code)
                .onSuccess { done(it) }
                .onFailure { failed = it.message }
            joining = false
        }
    }
    val camera = rememberLauncherForActivityResult(ScanContract()) { result -> result.contents?.let(take) }
    Column(Modifier.padding(horizontal = 24.dp).fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally) {
        Text("On the other device run drop add, or tap Add in drop there, and take the code it shows.", textAlign = TextAlign.Center)
        Spacer(Modifier.height(16.dp))
        Button(
            onClick = {
                camera.launch(
                    ScanOptions().setDesiredBarcodeFormats(ScanOptions.QR_CODE).setPrompt("Point at the code drop is showing")
                        .setBeepEnabled(false).setOrientationLocked(false),
                )
            },
            enabled = !joining,
            modifier = Modifier.fillMaxWidth().height(52.dp),
        ) {
            Icon(Icons.Filled.QrCodeScanner, null)
            Spacer(Modifier.width(10.dp))
            Text("Scan their code")
        }
        Spacer(Modifier.height(16.dp))
        OutlinedTextField(typed, { typed = it }, placeholder = { Text("or type it: abcd-efgh-ijkl") }, singleLine = true, textStyle = Mono, modifier = Modifier.fillMaxWidth())
        Spacer(Modifier.height(10.dp))
        FilledTonalButton(onClick = { take(typed) }, enabled = !joining && typed.isNotBlank(), modifier = Modifier.fillMaxWidth()) { Text("Add") }
        Spacer(Modifier.height(12.dp))
        if (joining) CircularProgressIndicator(Modifier.size(22.dp), strokeWidth = 2.dp)
        failed?.let { Banner(it, error = true) }
        Spacer(Modifier.height(24.dp))
    }
}

/**
 * Asking one device something — to be added, to become one of your machines, to take this phone
 * into theirs — and waiting while its person says yes, with the number both screens show.
 */
@Composable
fun AskDialog(id: String, name: String, kind: String, title: String, says: String, dismiss: () -> Unit, outcome: (String) -> Unit) {
    val scope = rememberCoroutineScope()
    var waiting by remember { mutableStateOf(false) }
    var check by remember { mutableStateOf("") }
    LaunchedEffect(id) { check = runCatching { Mobile.checkWith(id) }.getOrDefault("") }

    AlertDialog(
        onDismissRequest = { if (!waiting) dismiss() },
        title = { Text(if (waiting) "Waiting for $name" else title) },
        text = {
            Column {
                Text(if (waiting) "On $name, say yes. Check its screen shows this number:" else says)
                if (waiting) {
                    Spacer(Modifier.height(12.dp))
                    Text(check, fontSize = 32.sp, fontWeight = FontWeight.Bold, color = MaterialTheme.colorScheme.primary)
                    Spacer(Modifier.height(12.dp))
                    CircularProgressIndicator(Modifier.size(22.dp), strokeWidth = 2.dp)
                }
            }
        },
        confirmButton = {
            if (!waiting) {
                Button(onClick = {
                    waiting = true
                    scope.launch {
                        Drop.invite(id, kind)
                            .onSuccess {
                                outcome(
                                    when (kind) {
                                        "mine" -> "$it is one of your machines now"
                                        "join" -> "This phone is one of $it's machines now"
                                        else -> "Added $it"
                                    },
                                )
                            }
                            .onFailure { outcome(it.message ?: "It did not happen") }
                        waiting = false
                        Drop.bump()
                        dismiss()
                    }
                }) { Text("Ask") }
            }
        },
        dismissButton = { if (!waiting) TextButton(onClick = dismiss) { Text("Cancel") } },
    )
}

/**
 * The prompt for a YubiKey, whenever something on the phone needs one to sign: it watches for the
 * key held to the phone and for one plugged in, for as long as it is up.
 */
@Composable
fun YubiKeyPrompt() {
    val ask by dev.bresilla.drop.YubiKey.asking.collectAsState()
    val current = ask ?: return
    val context = LocalContext.current
    val activity = context.findActivity()

    DisposableEffect(current) {
        val kit = YubiKitManager(context.applicationContext)
        kit.startUsbDiscovery(UsbConfiguration()) { device -> dev.bresilla.drop.YubiKey.found(device) }
        activity?.let { runCatching { kit.startNfcDiscovery(NfcConfiguration(), it) { device -> dev.bresilla.drop.YubiKey.found(device) } } }
        onDispose {
            kit.stopUsbDiscovery()
            activity?.let { runCatching { kit.stopNfcDiscovery(it) } }
        }
    }

    AlertDialog(
        onDismissRequest = {},
        icon = { Icon(Icons.Filled.Key, null) },
        title = { Text("Hold your YubiKey to the phone") },
        text = { Text("Touch it to the back of the phone, or plug it in and touch it. It signs here, and your key never leaves it.") },
        confirmButton = {},
        dismissButton = { TextButton(onClick = { dev.bresilla.drop.YubiKey.cancel() }) { Text("Cancel") } },
    )
}

/** The activity a context belongs to, which is what NFC needs to read anything. */
private fun android.content.Context.findActivity(): android.app.Activity? {
    var at: android.content.Context? = this
    while (at is android.content.ContextWrapper) {
        if (at is android.app.Activity) return at
        at = at.baseContext
    }
    return null
}

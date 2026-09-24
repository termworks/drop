package dev.bresilla.drop.ui

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.QrCodeScanner
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Badge
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import dev.bresilla.drop.Drop
import dev.bresilla.drop.Knock
import dev.bresilla.drop.Machine
import dev.bresilla.drop.Me
import dev.bresilla.drop.PathState
import kotlinx.coroutines.launch

/** The two ways a machine becomes yours: this phone joining, or another computer being added. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AddMachineScreen(go: (Screen) -> Unit, back: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var me by remember { mutableStateOf<Me?>(null) }
    var taking by remember { mutableStateOf(false) }
    var told by remember { mutableStateOf<String?>(null) }
    var failed by remember { mutableStateOf<String?>(null) }
    var pasting by remember { mutableStateOf(false) }

    LaunchedEffect(Unit) { me = Drop.self() }

    // One scan for both steps: a pairing code pairs, and a code from vouch or export makes this phone theirs.
    val take: (String) -> Unit = { code ->
        taking = true
        failed = null
        scope.launch {
            if (Drop.owning(code)) {
                Drop.take(context.applicationContext, code)
                    .onSuccess { told = it; me = Drop.self() }
                    .onFailure { failed = it.message }
            } else {
                Drop.call { it.join(code.trim()) }
                    .onSuccess { told = "Paired with $it. Now run the vouch command there, and scan what it shows." }
                    .onFailure { failed = it.message }
            }
            taking = false
        }
    }
    val camera = rememberLauncherForActivityResult(ScanContract()) { result -> result.contents?.let(take) }
    val scan = {
        camera.launch(
            ScanOptions()
                .setDesiredBarcodeFormats(ScanOptions.QR_CODE)
                .setPrompt("Point at the code drop is showing")
                .setBeepEnabled(false)
                .setOrientationLocked(false),
        )
    }
    val phone = me?.name ?: "this-phone"

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
                    title = "Make this phone one of your machines",
                    says = "On a computer that has your key, pair with this phone, then have it vouch for it. Scan each code it shows:",
                    commands = listOf(
                        "drop peer pair" to "first, unless it knows this phone already",
                        "drop me user vouch $phone" to "your key stays on the computer",
                        "drop me user export" to "or instead: copies your key onto this phone",
                    ),
                ) {
                    Row(horizontalArrangement = Arrangement.spacedBy(10.dp), verticalAlignment = Alignment.CenterVertically) {
                        Button(onClick = scan, enabled = !taking) {
                            Icon(Icons.Filled.QrCodeScanner, null, Modifier.size(18.dp))
                            Spacer(Modifier.width(6.dp))
                            Text("Scan")
                        }
                        TextButton(onClick = { pasting = true }, enabled = !taking) { Text("Paste") }
                        if (taking) CircularProgressIndicator(Modifier.size(22.dp), strokeWidth = 2.dp)
                    }
                    told?.let { Banner(it, Modifier.padding(top = 8.dp)) }
                    failed?.let { Banner(it, Modifier.padding(top = 8.dp), error = true) }
                }
            }
            item {
                WayIn(
                    title = "Add another computer of yours",
                    says = "Pair with it first. If it already carries your key it lands here straight away; if it has a key of its own, a computer with yours vouches for it.",
                    commands = listOf(
                        "drop peer pair" to "on it, then scan what it shows",
                        "drop me user vouch <it>" to "on a computer with your key",
                        "drop me user take <code>" to "back on it, with that code",
                    ),
                ) {
                    Button(onClick = { go(Screen.Pair(scan = true)) }) {
                        Icon(Icons.Filled.QrCodeScanner, null, Modifier.size(18.dp))
                        Spacer(Modifier.width(6.dp))
                        Text("Scan its pairing code")
                    }
                }
            }
        }
    }

    if (pasting) {
        var pasted by remember { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { pasting = false },
            title = { Text("Paste the code") },
            text = { OutlinedTextField(pasted, { pasted = it }, placeholder = { Text("a pairing ticket, or drop://badge/…") }, textStyle = Mono) },
            confirmButton = {
                TextButton(enabled = pasted.isNotBlank(), onClick = { pasting = false; take(pasted) }) { Text("Take it") }
            },
            dismissButton = { TextButton(onClick = { pasting = false }) { Text("Cancel") } },
        )
    }
}

/** One way of adding a machine: what it does, the commands it takes, and what to press here. */
@Composable
private fun WayIn(title: String, says: String, commands: List<Pair<String, String>>, actions: @Composable () -> Unit) {
    Card(
        modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp).fillMaxWidth(),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
        shape = RoundedCornerShape(24.dp),
    ) {
        Column(Modifier.padding(20.dp)) {
            Text(title, style = MaterialTheme.typography.titleLarge)
            Spacer(Modifier.height(8.dp))
            Text(says, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(Modifier.height(10.dp))
            commands.forEach { (command, why) ->
                Text(command, style = Mono, color = MaterialTheme.colorScheme.primary)
                Text(why, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                Spacer(Modifier.height(6.dp))
            }
            Spacer(Modifier.height(8.dp))
            actions()
        }
    }
}

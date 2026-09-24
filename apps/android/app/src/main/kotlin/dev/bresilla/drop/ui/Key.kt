package dev.bresilla.drop.ui

import android.net.Uri
import android.provider.OpenableColumns
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Description
import androidx.compose.material.icons.filled.Key
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.ui.unit.dp
import dev.bresilla.drop.Drop
import kotlinx.coroutines.launch

/**
 * Who you are, chosen: the key in your YubiKey, read off it with its PIN, or an SSH key file handed
 * to the phone. Every machine of yours that holds the same key finds this phone by itself; one that
 * does not is added with a code.
 */
@Composable
fun ChooseKey(done: () -> Unit, said: (String) -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var askingPin by remember { mutableStateOf(false) }
    var pin by remember { mutableStateOf("") }

    val pick = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri: Uri? ->
        if (uri == null) return@rememberLauncherForActivityResult
        done()
        scope.launch {
            val name = context.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use {
                if (it.moveToFirst()) it.getString(0) else null
            } ?: "id_ed25519"
            val raw = runCatching { context.contentResolver.openInputStream(uri)?.use { it.readBytes() } }.getOrNull()
            if (raw == null) {
                said("Could not read that file")
                return@launch
            }
            Drop.useKeyFile(name, raw)
                .onSuccess { said("You are $it now. Your machines holding this key find the phone by themselves.") }
                .onFailure { said(it.message ?: "Could not use that key") }
        }
    }

    if (askingPin) {
        AlertDialog(
            onDismissRequest = done,
            icon = { Icon(Icons.Filled.Key, null) },
            title = { Text("Your YubiKey's PIN") },
            text = {
                Column {
                    Text(
                        "It unlocks which key on the YubiKey is yours. Then hold it to the phone — and once more, to sign this phone's badge.",
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(12.dp))
                    OutlinedTextField(
                        pin,
                        { pin = it },
                        singleLine = true,
                        label = { Text("PIN") },
                        visualTransformation = PasswordVisualTransformation(),
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.NumberPassword),
                    )
                }
            },
            confirmButton = {
                TextButton(enabled = pin.length >= 4, onClick = {
                    val typed = pin.toCharArray()
                    pin = ""
                    done()
                    scope.launch {
                        Drop.useYubiKey(typed)
                            .onSuccess { said("You are the key in your YubiKey now. Your machines holding it find the phone by themselves.") }
                            .onFailure { said(it.message ?: "Could not read the YubiKey") }
                    }
                }) { Text("Read it") }
            },
            dismissButton = { TextButton(onClick = done) { Text("Cancel") } },
        )
        return
    }

    AlertDialog(
        onDismissRequest = done,
        title = { Text("Who you are") },
        text = {
            Column {
                Text(
                    "Every machine of yours that holds the same key finds this phone by itself. One that does not is added with a code.",
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(12.dp))
                Choice(Icons.Filled.Key, "My YubiKey", "Hold it to the phone; its PIN says which key is yours") { askingPin = true }
                Choice(Icons.Filled.Description, "An SSH key file", "A private key, like id_ed25519, without a passphrase") { pick.launch(arrayOf("*/*")) }
            }
        },
        confirmButton = {},
        dismissButton = { TextButton(onClick = done) { Text("Cancel") } },
    )
}

@Composable
private fun Choice(icon: androidx.compose.ui.graphics.vector.ImageVector, title: String, says: String, onClick: () -> Unit) {
    ListItem(
        modifier = Modifier.clip(RoundedCornerShape(16.dp)).clickable(onClick = onClick),
        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
        leadingContent = { Icon(icon, null) },
        headlineContent = { Text(title) },
        supportingContent = { Text(says, color = MaterialTheme.colorScheme.onSurfaceVariant) },
    )
}

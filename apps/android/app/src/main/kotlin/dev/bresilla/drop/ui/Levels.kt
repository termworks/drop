package dev.bresilla.drop.ui

import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Group
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.Public
import androidx.compose.material.icons.filled.Star
import androidx.compose.material.icons.filled.Tune
import androidx.compose.material3.AssistChip
import androidx.compose.material3.AssistChipDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.dp

/** One step on the ladder of who may reach a path. */
data class Step(val level: String, val title: String, val says: String, val icon: ImageVector, val tint: Color)

/** The ladder, narrowest first. */
val Steps = listOf(
    Step("me", "Only me", "Your own machines, and nobody else", Icons.Filled.Lock, Color(0xFF5B4FD6)),
    Step("trusted", "Trusted", "You, and the people you trust", Icons.Filled.Star, Color(0xFFE0A21F)),
    Step("paired", "Paired", "Everybody you paired with", Icons.Filled.Group, Color(0xFF2F6FDB)),
    Step("anyone", "Public", "Anyone who knows this machine's id", Icons.Filled.Public, Color(0xFFD84F7A)),
)

private val Custom = Step("custom", "Custom", "Its config names who, in its own way", Icons.Filled.Tune, Color(0xFF7B5CC4))

fun stepOf(level: String): Step = Steps.firstOrNull { it.level == level } ?: Custom

/** Who may reach a path, as a chip to tap for more. */
@Composable
fun LevelChip(level: String, onClick: () -> Unit) {
    val step = stepOf(level)
    AssistChip(
        onClick = onClick,
        label = { Text(step.title) },
        leadingIcon = { Icon(step.icon, null, Modifier.size(16.dp), tint = step.tint) },
        colors = AssistChipDefaults.assistChipColors(containerColor = step.tint.copy(alpha = 0.10f)),
        border = null,
    )
}

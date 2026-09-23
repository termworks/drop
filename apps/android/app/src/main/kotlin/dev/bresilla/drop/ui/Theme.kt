package dev.bresilla.drop.ui

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp

private val Light = lightColorScheme(
    primary = Color(0xFF5B4FD6),
    onPrimary = Color.White,
    primaryContainer = Color(0xFFE4DFFF),
    onPrimaryContainer = Color(0xFF170065),
    secondary = Color(0xFF2F6FDB),
    onSecondary = Color.White,
    secondaryContainer = Color(0xFFD8E2FF),
    onSecondaryContainer = Color(0xFF001A43),
    tertiary = Color(0xFF00897B),
    tertiaryContainer = Color(0xFFB2F1E8),
    onTertiaryContainer = Color(0xFF00201C),
    background = Color(0xFFFBF8FF),
    onBackground = Color(0xFF1B1B21),
    surface = Color(0xFFFBF8FF),
    onSurface = Color(0xFF1B1B21),
    surfaceVariant = Color(0xFFE5E1EC),
    onSurfaceVariant = Color(0xFF47464F),
    surfaceContainerLowest = Color.White,
    surfaceContainerLow = Color(0xFFF5F2FA),
    surfaceContainer = Color(0xFFEFECF4),
    surfaceContainerHigh = Color(0xFFE9E7EF),
    surfaceContainerHighest = Color(0xFFE4E1E9),
    outline = Color(0xFF787680),
    outlineVariant = Color(0xFFC8C5D0),
    error = Color(0xFFBA1A1A),
)

private val Dark = darkColorScheme(
    primary = Color(0xFFC6BFFF),
    onPrimary = Color(0xFF2A1A9E),
    primaryContainer = Color(0xFF4335BD),
    onPrimaryContainer = Color(0xFFE4DFFF),
    secondary = Color(0xFFAEC6FF),
    onSecondary = Color(0xFF002E6B),
    secondaryContainer = Color(0xFF1B55C0),
    onSecondaryContainer = Color(0xFFD8E2FF),
    tertiary = Color(0xFF5DDBC8),
    tertiaryContainer = Color(0xFF00504A),
    onTertiaryContainer = Color(0xFFB2F1E8),
    background = Color(0xFF131318),
    onBackground = Color(0xFFE4E1E9),
    surface = Color(0xFF131318),
    onSurface = Color(0xFFE4E1E9),
    surfaceVariant = Color(0xFF47464F),
    onSurfaceVariant = Color(0xFFC8C5D0),
    surfaceContainerLowest = Color(0xFF0E0E13),
    surfaceContainerLow = Color(0xFF1B1B21),
    surfaceContainer = Color(0xFF1F1F25),
    surfaceContainerHigh = Color(0xFF2A292F),
    surfaceContainerHighest = Color(0xFF35343A),
    outline = Color(0xFF928F9A),
    outlineVariant = Color(0xFF47464F),
    error = Color(0xFFFFB4AB),
)

private val Type = Typography().let { base ->
    base.copy(
        displaySmall = base.displaySmall.copy(fontWeight = FontWeight.SemiBold),
        headlineMedium = base.headlineMedium.copy(fontWeight = FontWeight.SemiBold),
        headlineSmall = base.headlineSmall.copy(fontWeight = FontWeight.SemiBold),
        titleLarge = base.titleLarge.copy(fontWeight = FontWeight.SemiBold),
        titleMedium = base.titleMedium.copy(fontWeight = FontWeight.SemiBold),
        labelLarge = base.labelLarge.copy(fontWeight = FontWeight.SemiBold),
    )
}

/** Small print under a heading: an id, a path, a time. */
val Mono = TextStyle(fontFamily = androidx.compose.ui.text.font.FontFamily.Monospace, fontSize = 12.sp)

@Composable
fun DropTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = if (isSystemInDarkTheme()) Dark else Light,
        typography = Type,
        content = content,
    )
}

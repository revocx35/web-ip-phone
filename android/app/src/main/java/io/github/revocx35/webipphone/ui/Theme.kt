package io.github.revocx35.webipphone.ui

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

val CallGreen = Color(0xFF16A34A)
val HangupRed = Color(0xFFDC2626)

private val Light = lightColorScheme(
    primary = Color(0xFF0F766E), onPrimary = Color.White, primaryContainer = Color(0xFFD5F5EF), onPrimaryContainer = Color(0xFF053B37),
    secondary = Color(0xFF475569), background = Color(0xFFF4F6F9), surface = Color.White, surfaceVariant = Color(0xFFEEF2F6),
    surfaceContainer = Color(0xFFF0F3F7), error = Color(0xFFDC2626),
)

private val Dark = darkColorScheme(
    primary = Color(0xFF2DD4BF), onPrimary = Color(0xFF03211F), primaryContainer = Color(0xFF0F3A37), onPrimaryContainer = Color(0xFFB9F5EA),
    secondary = Color(0xFF94A3B8), background = Color(0xFF0A111E), surface = Color(0xFF111A2A), surfaceVariant = Color(0xFF1D2A40),
    surfaceContainer = Color(0xFF162133), error = Color(0xFFF87171),
)

@Composable
fun WebPhoneTheme(content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = if (isSystemInDarkTheme()) Dark else Light, content = content)
}

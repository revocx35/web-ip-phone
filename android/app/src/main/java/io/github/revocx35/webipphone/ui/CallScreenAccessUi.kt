package io.github.revocx35.webipphone.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.LifecycleResumeEffect
import io.github.revocx35.webipphone.WebPhoneApp
import io.github.revocx35.webipphone.phone.CallScreenAccess

/** Re-evaluates permission checks whenever the screen comes back (e.g. from Android settings). */
@Composable
fun rememberResumeTick(): Int {
    var tick by remember { mutableIntStateOf(0) }
    LifecycleResumeEffect(Unit) {
        tick++
        onPauseOrDispose { }
    }
    return tick
}

private const val EXPLANATION = "When your phone is locked or the screen is off, Android only shows the incoming " +
    "call screen if Web IP Phone may use full-screen notifications. Without it, calls just ring with a notification."

/** Once after sign-in: explains and opens the setting if calls cannot show on the lock screen. */
@Composable
fun CallScreenPrompt(app: WebPhoneApp) {
    val ctx = LocalContext.current
    val tick = rememberResumeTick()
    var show by remember { mutableStateOf(!app.prefs.askedCallScreen) }
    val limited = remember(tick) { CallScreenAccess.limited(ctx) }
    if (!show || !limited) return
    AlertDialog(
        onDismissRequest = { show = false; app.prefs.askedCallScreen = true },
        title = { Text("Show calls on the lock screen") },
        text = { Text("$EXPLANATION\n\nAllow \"full-screen notifications\" for Web IP Phone on the next screen.") },
        confirmButton = {
            TextButton({
                show = false
                app.prefs.askedCallScreen = true
                runCatching { ctx.startActivity(CallScreenAccess.fullScreenSettings(ctx)) }
            }, Modifier.testTag("callscreen-allow")) { Text("Allow") }
        },
        dismissButton = { TextButton({ show = false; app.prefs.askedCallScreen = true }) { Text("Not now") } },
    )
}

/** Keypad banner while incoming calls can only show as a notification. */
@Composable
fun CallScreenBanner() {
    val ctx = LocalContext.current
    val tick = rememberResumeTick()
    val limited = remember(tick) { CallScreenAccess.limited(ctx) }
    if (!limited) return
    Surface(color = MaterialTheme.colorScheme.errorContainer, shape = RoundedCornerShape(12.dp),
        modifier = Modifier.fillMaxWidth().padding(top = 10.dp).testTag("callscreen-banner")) {
        Row(Modifier.padding(start = 14.dp, end = 4.dp, top = 6.dp, bottom = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            Text("Incoming calls can't show on the lock screen", Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onErrorContainer)
            TextButton({ runCatching { ctx.startActivity(CallScreenAccess.fullScreenSettings(ctx)) } }) { Text("Allow") }
        }
    }
}

/** Settings rows for both ways a call can reach the screen. */
@Composable
fun CallScreenSettings() {
    val ctx = LocalContext.current
    val tick = rememberResumeTick()
    val fullScreen = remember(tick) { CallScreenAccess.fullScreenAllowed(ctx) }
    val overlay = remember(tick) { CallScreenAccess.overlayAllowed(ctx) }
    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Text("Call screen when locked", fontWeight = FontWeight.SemiBold)
        Text(
            when {
                fullScreen -> "Allowed: incoming calls turn the screen on and show the call screen."
                overlay -> "Full-screen notifications are off, but the app opens the call screen itself (Display over other apps)."
                else -> EXPLANATION
            },
            style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        if (!fullScreen) {
            OutlinedButton({ runCatching { ctx.startActivity(CallScreenAccess.fullScreenSettings(ctx)) } }) {
                Text("Allow full-screen notifications")
            }
            if (!overlay) {
                Text("If your phone does not offer that setting or calls still don't show, allow \"Display over other apps\" " +
                    "instead: the app then opens the call screen itself, also when you unlock the phone.",
                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                OutlinedButton({ runCatching { ctx.startActivity(CallScreenAccess.overlaySettings(ctx)) } }) {
                    Text("Allow display over other apps")
                }
            }
        }
    }
}

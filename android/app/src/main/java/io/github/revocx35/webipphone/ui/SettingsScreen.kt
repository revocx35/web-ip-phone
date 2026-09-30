package io.github.revocx35.webipphone.ui

import android.annotation.SuppressLint
import android.app.NotificationManager
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.PowerManager
import android.provider.Settings
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.core.net.toUri
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.testTagsAsResourceId
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import io.github.revocx35.webipphone.BuildConfig
import io.github.revocx35.webipphone.Session
import io.github.revocx35.webipphone.WebPhoneApp
import io.github.revocx35.webipphone.net.PhonesResponse
import kotlinx.coroutines.launch

@Composable
private fun Section(title: String, content: @Composable () -> Unit) {
    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Text(title, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
            content()
        }
    }
}

@SuppressLint("BatteryLife")
@Composable
fun SettingsScreen(app: WebPhoneApp) {
    val ctx = LocalContext.current
    val session by app.session.collectAsState()
    val st by app.phone.state.collectAsState()
    val me = (session as? Session.Active)?.me
    var background by remember { mutableStateOf(app.prefs.backgroundCalls) }
    var confirmSignOut by remember { mutableStateOf(false) }
    var refresh by remember { mutableIntStateOf(0) }
    val pm = ctx.getSystemService(PowerManager::class.java)
    val nm = ctx.getSystemService(NotificationManager::class.java)

    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
        Text("Settings", style = MaterialTheme.typography.titleLarge)
        Section("Account") {
            Text(me?.let { (it.displayName.ifBlank { it.username }) + if (it.isAdmin) " (administrator)" else "" } ?: app.prefs.username ?: "")
            Text(app.prefs.serverUrl ?: "", color = MaterialTheme.colorScheme.onSurfaceVariant)
            app.prefs.pinnedCert?.let {
                Text("Trusted certificate (SHA-256):", style = MaterialTheme.typography.labelMedium)
                Text(it, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall)
            }
            Text("Two-factor authentication: " + if (me?.totpEnabled == true) "on" else "off (set it up in the web app)",
                style = MaterialTheme.typography.bodySmall)
            OutlinedButton({ confirmSignOut = true }, Modifier.testTag("signout")) { Text("Sign out") }
        }
        Section("Incoming calls") {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text("Receive calls when the app is closed")
                    Text("Keeps a connection to your server (with a silent notification). Uses some battery.",
                        style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                Spacer(Modifier.width(10.dp))
                Switch(background, { background = it; app.setBackgroundCalls(it) }, Modifier.testTag("background"))
            }
            if (background && !pm.isIgnoringBatteryOptimizations(ctx.packageName)) {
                Text("Battery optimization may stop the connection while the phone sleeps.", style = MaterialTheme.typography.bodySmall)
                OutlinedButton({
                    runCatching {
                        ctx.startActivity(Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, "package:${ctx.packageName}".toUri()))
                    }
                }) { Text("Allow running in the background") }
            }
            if (Build.VERSION.SDK_INT >= 34 && !nm.canUseFullScreenIntent()) {
                Text("Full-screen incoming calls are not allowed: calls only show as a notification.", style = MaterialTheme.typography.bodySmall)
                OutlinedButton({
                    runCatching { ctx.startActivity(Intent(Settings.ACTION_MANAGE_APP_USE_FULL_SCREEN_INTENT, "package:${ctx.packageName}".toUri())) }
                }) { Text("Allow full-screen calls") }
            }
            st.selectedPhone?.let { p ->
                Text("Current phone: ${p.title} (${p.sipUser} @ ${p.pbxName}) - " + when (p.reg.status) {
                    "registered" -> "registered"
                    "failed" -> "registration failed: ${p.reg.error ?: ""}"
                    "registering" -> "registering"
                    else -> if (p.register) "not registered" else "outgoing calls only"
                }, style = MaterialTheme.typography.bodySmall)
            }
        }
        MyPhones(app, refresh) { refresh++ }
        Section("About") {
            Text("Web IP Phone ${BuildConfig.VERSION_NAME}" + if (st.serverVersion.isNotBlank()) " · server ${st.serverVersion}" else "")
            TextButton({ runCatching { ctx.startActivity(Intent(Intent.ACTION_VIEW, "https://github.com/revocx35/web-ip-phone".toUri())) } }) {
                Text("Source code and documentation")
            }
        }
    }

    if (confirmSignOut) {
        AlertDialog(
            onDismissRequest = { confirmSignOut = false },
            title = { Text("Sign out?") },
            text = { Text("You will not receive calls on this device until you sign in again.") },
            confirmButton = { TextButton({ confirmSignOut = false; app.signOut() }) { Text("Sign out") } },
            dismissButton = { TextButton({ confirmSignOut = false }) { Text("Cancel") } },
        )
    }
}

/** Phones the user added with their own SIP credentials (PBXs with "any SIP account" access). */
@Composable
private fun MyPhones(app: WebPhoneApp, refresh: Int, onChanged: () -> Unit) {
    val scope = rememberCoroutineScope()
    var data by remember { mutableStateOf<PhonesResponse?>(null) }
    var adding by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(refresh) { runCatching { app.api?.phones() }.onSuccess { data = it }.onFailure { error = it.message } }
    val d = data ?: return
    val addable = d.pbxs.filter { it.canAddPhones }
    Section("My phones") {
        if (d.phones.isEmpty()) Text("No phones.", color = MaterialTheme.colorScheme.onSurfaceVariant)
        d.phones.forEach { p ->
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text("${p.label.ifBlank { p.sipUser }} · ${p.sipUser}", fontWeight = FontWeight.Medium)
                    Text(p.pbxName + if (p.owned) " · your SIP account" else " · from your administrator",
                        style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                if (p.owned) TextButton({
                    scope.launch { runCatching { app.api?.deletePhone(p.id) }.onFailure { error = it.message }; onChanged() }
                }) { Text("Delete") }
            }
            HorizontalDivider()
        }
        ErrorText(error)
        if (addable.isNotEmpty()) Button({ adding = true }) { Text("Add phone") }
    }
    if (adding) AddPhoneDialog(app, addable.map { it.id to it.name }, onDone = { adding = false; onChanged() }, onCancel = { adding = false })
}

@Composable
private fun AddPhoneDialog(app: WebPhoneApp, pbxs: List<Pair<Long, String>>, onDone: () -> Unit, onCancel: () -> Unit) {
    val scope = rememberCoroutineScope()
    var pbx by remember { mutableStateOf(pbxs.first()) }
    var sipUser by remember { mutableStateOf("") }
    var authUser by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var name by remember { mutableStateOf("") }
    var label by remember { mutableStateOf("") }
    var register by remember { mutableStateOf(true) }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    AlertDialog(
        onDismissRequest = onCancel,
        title = { Text("Add phone") },
        text = {
            // Dialogs have their own composition: expose test tags here too (UI Automator e2e).
            Column(Modifier.semantics { testTagsAsResourceId = true }.verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(8.dp)) {
                if (pbxs.size > 1) {
                    pbxs.forEach { p -> Row(verticalAlignment = Alignment.CenterVertically) {
                        androidx.compose.material3.RadioButton(pbx == p, { pbx = p }); Text(p.second)
                    } }
                } else Text("PBX: ${pbx.second}")
                OutlinedTextField(sipUser, { sipUser = it.trim() }, label = { Text("Extension / SIP user") }, singleLine = true,
                    modifier = Modifier.testTag("add-sipuser"))
                OutlinedTextField(password, { password = it }, label = { Text("SIP password") }, singleLine = true,
                    visualTransformation = PasswordVisualTransformation(), modifier = Modifier.testTag("add-password"))
                OutlinedTextField(authUser, { authUser = it.trim() }, label = { Text("Auth username (optional)") }, singleLine = true)
                OutlinedTextField(name, { name = it }, label = { Text("Caller ID name (optional)") }, singleLine = true)
                OutlinedTextField(label, { label = it }, label = { Text("Label (optional)") }, singleLine = true)
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text("Receive incoming calls", Modifier.weight(1f))
                    Switch(register, { register = it })
                }
                ErrorText(error)
            }
        },
        confirmButton = {
            TextButton({
                busy = true
                scope.launch {
                    try {
                        app.api?.addPhone(pbx.first, sipUser, authUser, password, name.trim(), label.trim(), register)
                        onDone()
                    } catch (e: Exception) {
                        error = e.message
                    } finally {
                        busy = false
                    }
                }
            }, enabled = !busy && sipUser.isNotBlank() && password.isNotEmpty()) { Text(if (busy) "Checking…" else "Check & save") }
        },
        dismissButton = { TextButton(onCancel) { Text("Cancel") } },
    )
}

package io.github.revocx35.webipphone.ui

import android.content.Intent
import android.net.Uri
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.systemBarsPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import io.github.revocx35.webipphone.R
import io.github.revocx35.webipphone.Session
import io.github.revocx35.webipphone.WebPhoneApp
import io.github.revocx35.webipphone.net.Api
import io.github.revocx35.webipphone.net.Me
import io.github.revocx35.webipphone.net.UntrustedCertificateException
import io.github.revocx35.webipphone.net.normalizeServerUrl
import io.github.revocx35.webipphone.net.untrustedCertificate
import java.text.DateFormat
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

@Composable
private fun Brand() {
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        Surface(shape = MaterialTheme.shapes.medium, color = MaterialTheme.colorScheme.primary, modifier = Modifier.size(48.dp)) {
            Image(painterResource(R.drawable.ic_launcher_foreground), contentDescription = null)
        }
        Text("Web IP Phone", style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.SemiBold)
    }
}

@Composable
private fun AuthScaffold(content: @Composable () -> Unit) {
    Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
        Column(
            Modifier.fillMaxSize().systemBarsPadding().imePadding().verticalScroll(rememberScrollState()).padding(24.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            Spacer(Modifier.height(24.dp))
            Brand()
            Spacer(Modifier.height(8.dp))
            content()
        }
    }
}

/** Sign-in flow: server address (with certificate trust) -> username/password -> 2FA code. */
@Composable
fun SignInFlow(app: WebPhoneApp) {
    val scope = rememberCoroutineScope()
    val ctx = LocalContext.current
    val reason by app.signOutReason.collectAsState()
    var url by rememberSaveable { mutableStateOf(app.prefs.serverUrl ?: "") }
    var normalized by rememberSaveable { mutableStateOf<String?>(null) }
    var pin by rememberSaveable { mutableStateOf<String?>(null) }
    var username by rememberSaveable { mutableStateOf(app.prefs.username ?: "") }
    var password by remember { mutableStateOf("") }
    var ticket by rememberSaveable { mutableStateOf<String?>(null) }
    var code by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(reason) }
    var untrusted by remember { mutableStateOf<Pair<String, UntrustedCertificateException>?>(null) }
    var needLan by remember { mutableStateOf(false) }
    val lanLauncher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { ok ->
        needLan = false
        if (!ok) error = "Allow \"Local network access\" for Web IP Phone to reach a server on your network."
    }

    suspend fun probe(u: String, p: String?) {
        val info = Api(u, p).info()
        if (info.setupRequired) throw Exception("This server is not set up yet. Open it in a web browser and create the admin account first.")
        if (info.api < 1) throw Exception("This does not look like a Web IP Phone server.")
    }

    fun connect() {
        error = null
        scope.launch {
            busy = true
            try {
                val u = normalizeServerUrl(url)
                val host = android.net.Uri.parse(u).host ?: ""
                if (Build.VERSION.SDK_INT >= 37 && !LocalNetwork.granted(ctx) && withContext(Dispatchers.IO) { LocalNetwork.isLan(host) }) {
                    needLan = true
                    return@launch
                }
                val samePin = if (u == app.prefs.serverUrl) app.prefs.pinnedCert else null
                try {
                    probe(u, samePin)
                    pin = samePin
                    normalized = u
                } catch (e: Exception) {
                    val cert = e.untrustedCertificate() ?: throw e
                    untrusted = u to cert
                }
            } catch (e: Exception) {
                error = e.message ?: e.toString()
            } finally {
                busy = false
            }
        }
    }

    fun login() {
        error = null
        val u = normalized ?: return
        scope.launch {
            busy = true
            try {
                val api = Api(u, pin)
                val r = if (ticket == null) api.login(username.trim(), password, "${Build.MANUFACTURER} ${Build.MODEL}".trim())
                else api.mfa(ticket!!, code.trim())
                when {
                    r.mfaRequired && r.ticket != null -> { ticket = r.ticket; password = "" }
                    r.token != null && r.user != null -> { password = ""; app.completeLogin(u, pin, r.token, r.user) }
                    else -> error = "Unexpected answer from the server"
                }
            } catch (e: Exception) {
                val msg = e.message ?: e.toString()
                if (ticket != null && msg.contains("expired", true)) { ticket = null; code = "" }
                error = msg
            } finally {
                busy = false
            }
        }
    }

    AuthScaffold {
        when {
            normalized == null -> {
                Text("Connect to your server", style = MaterialTheme.typography.titleLarge)
                Text("The address of your Web IP Phone server, as you open it in a browser.", color = MaterialTheme.colorScheme.onSurfaceVariant)
                OutlinedTextField(url, { url = it.trim() }, Modifier.fillMaxWidth().testTag("server"), label = { Text("Server address") },
                    placeholder = { Text("https://phone.example.com") }, singleLine = true,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Go))
                ErrorText(error)
                Button(::connect, Modifier.fillMaxWidth().height(52.dp), enabled = !busy && url.isNotBlank()) {
                    if (busy) CircularProgressIndicator(Modifier.size(22.dp), strokeWidth = 2.dp) else Text("Continue")
                }
            }
            ticket == null -> {
                Text("Sign in", style = MaterialTheme.typography.titleLarge)
                Text(normalized!!, color = MaterialTheme.colorScheme.onSurfaceVariant)
                OutlinedTextField(username, { username = it }, Modifier.fillMaxWidth().testTag("username"), label = { Text("Username") }, singleLine = true,
                    keyboardOptions = KeyboardOptions(imeAction = ImeAction.Next))
                OutlinedTextField(password, { password = it }, Modifier.fillMaxWidth().testTag("password"), label = { Text("Password") }, singleLine = true,
                    visualTransformation = PasswordVisualTransformation(),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = ImeAction.Go))
                ErrorText(error)
                Button(::login, Modifier.fillMaxWidth().height(52.dp).testTag("signin"), enabled = !busy && username.isNotBlank() && password.isNotEmpty()) {
                    if (busy) CircularProgressIndicator(Modifier.size(22.dp), strokeWidth = 2.dp) else Text("Sign in")
                }
                TextButton({ normalized = null; error = null }) { Text("Change server") }
            }
            else -> {
                Text("Two-factor authentication", style = MaterialTheme.typography.titleLarge)
                Text("Enter the 6-digit code from your authenticator app, or a recovery code.", color = MaterialTheme.colorScheme.onSurfaceVariant)
                OutlinedTextField(code, { code = it }, Modifier.fillMaxWidth().testTag("code"), label = { Text("Code") }, singleLine = true,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number, imeAction = ImeAction.Go))
                ErrorText(error)
                Button(::login, Modifier.fillMaxWidth().height(52.dp), enabled = !busy && code.isNotBlank()) {
                    if (busy) CircularProgressIndicator(Modifier.size(22.dp), strokeWidth = 2.dp) else Text("Verify")
                }
                TextButton({ ticket = null; code = ""; error = null }) { Text("Back") }
            }
        }
    }

    if (needLan) {
        AlertDialog(
            onDismissRequest = { needLan = false },
            title = { Text("Allow local network access") },
            text = { Text("This server is on your local network. Android asks for your permission before the app can connect to it.") },
            confirmButton = { TextButton({ lanLauncher.launch(LocalNetwork.PERMISSION) }) { Text("Allow") } },
            dismissButton = { TextButton({ needLan = false }) { Text("Cancel") } },
        )
    }

    untrusted?.let { (u, cert) ->
        AlertDialog(
            onDismissRequest = { untrusted = null },
            title = { Text(if (cert.pinMismatch) "The certificate changed!" else "Trust this server?") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(10.dp), modifier = Modifier.verticalScroll(rememberScrollState())) {
                    Text(
                        if (cert.pinMismatch) "The server shows a different certificate than the one you trusted before. " +
                            "Unless you replaced it, someone may be intercepting the connection. Do not continue on an untrusted network."
                        else "The server uses a certificate that is not issued by a known authority (normal for a self-signed setup). " +
                            "Compare its fingerprint with the one shown in the web app under Admin > Overview, or in the server log."
                    )
                    Text("SHA-256 fingerprint", fontWeight = FontWeight.SemiBold)
                    Text(cert.fingerprint, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall)
                    Text("Issued to: ${cert.subject}", style = MaterialTheme.typography.bodySmall)
                    Text("Valid until: ${DateFormat.getDateInstance().format(cert.notAfter)}", style = MaterialTheme.typography.bodySmall)
                }
            },
            confirmButton = {
                TextButton({
                    untrusted = null
                    scope.launch {
                        busy = true
                        try {
                            probe(u, cert.fingerprint)
                            pin = cert.fingerprint
                            normalized = u
                        } catch (e: Exception) {
                            error = e.message
                        } finally {
                            busy = false
                        }
                    }
                }) { Text(if (cert.pinMismatch) "Trust the new certificate" else "Trust") }
            },
            dismissButton = { TextButton({ untrusted = null }) { Text("Cancel") } },
        )
    }
}

@Composable
fun ErrorText(msg: String?) {
    if (msg != null) Text(msg, color = MaterialTheme.colorScheme.error, modifier = Modifier.testTag("error"))
}

/** An admin set a temporary password or requires 2FA: finish that before using the phone. */
@Composable
fun RestrictedFlow(app: WebPhoneApp, me: Me) {
    val scope = rememberCoroutineScope()
    val ctx = LocalContext.current
    var current by remember { mutableStateOf("") }
    var next by remember { mutableStateOf("") }
    var code by remember { mutableStateOf("") }
    var secret by remember { mutableStateOf<String?>(null) }
    var uri by remember { mutableStateOf<String?>(null) }
    var codes by remember { mutableStateOf<List<String>?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    val api = app.api ?: return

    AuthScaffold {
        if (me.mustChangePassword) {
            Text("Choose a new password", style = MaterialTheme.typography.titleLarge)
            Text("Your password was set by an administrator.", color = MaterialTheme.colorScheme.onSurfaceVariant)
            OutlinedTextField(current, { current = it }, Modifier.fillMaxWidth(), label = { Text("Current password") }, singleLine = true,
                visualTransformation = PasswordVisualTransformation())
            OutlinedTextField(next, { next = it }, Modifier.fillMaxWidth(), label = { Text("New password (${me.passwordMinLength}+ characters)") },
                singleLine = true, visualTransformation = PasswordVisualTransformation())
            ErrorText(error)
            Button({
                scope.launch {
                    try {
                        val m = api.changePassword(current, next)
                        app.session.value = Session.Active(m)
                    } catch (e: Exception) {
                        error = e.message
                    }
                }
            }, Modifier.fillMaxWidth().height(52.dp)) { Text("Change password") }
        } else if (codes != null) {
            Text("Save your recovery codes", style = MaterialTheme.typography.titleLarge)
            Text("Each code signs you in once if you lose your authenticator. They are shown only now.")
            Text(codes!!.joinToString("\n"), fontFamily = FontFamily.Monospace)
            Button({ app.refreshMe() }, Modifier.fillMaxWidth().height(52.dp)) { Text("I saved them") }
        } else {
            Text("Set up two-factor authentication", style = MaterialTheme.typography.titleLarge)
            Text("Your administrator requires a second factor. Add this account to an authenticator app, then enter the code it shows.",
                color = MaterialTheme.colorScheme.onSurfaceVariant)
            LaunchedEffect(Unit) {
                runCatching { api.totpSetup() }.onSuccess { secret = it.secret; uri = it.uri }.onFailure { error = it.message }
            }
            secret?.let { s ->
                Text("Key: " + s.chunked(4).joinToString(" "), fontFamily = FontFamily.Monospace)
                TextButton({
                    runCatching { ctx.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(uri))) }
                        .onFailure { error = "No authenticator app found; enter the key manually." }
                }) { Text("Open in authenticator app") }
            }
            OutlinedTextField(code, { code = it }, Modifier.fillMaxWidth(), label = { Text("6-digit code") }, singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number))
            ErrorText(error)
            Button({
                scope.launch {
                    try {
                        codes = api.totpEnable(code.trim()).recoveryCodes
                    } catch (e: Exception) {
                        error = e.message
                    }
                }
            }, Modifier.fillMaxWidth().height(52.dp), enabled = secret != null) { Text("Turn on") }
        }
        TextButton({ app.signOut() }) { Text("Sign out") }
    }
}

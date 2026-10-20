package io.github.revocx35.webipphone.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.systemBarsPadding
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
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
import androidx.compose.ui.unit.dp
import io.github.revocx35.webipphone.WebPhoneApp
import io.github.revocx35.webipphone.data.AuthentikSignIn
import io.github.revocx35.webipphone.net.AuthentikRequiredException
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.OkHttpClient
import java.io.IOException

/** The [AuthentikRequiredException] in this exception's chain, if the server wants an authentik sign-in. */
fun Throwable.authentikRequired(): AuthentikRequiredException? {
    var t: Throwable? = this
    while (t != null) {
        if (t is AuthentikRequiredException) return t
        t = t.cause
    }
    return null
}

/**
 * "Sign in with authentik" for a server behind authentik forward auth: opens authentik's login in the
 * browser, then [onSignedIn]. The client ID is the public OAuth client set up in authentik for this app.
 * [client] reaches the server (same certificate trust as the API).
 */
@Composable
fun AuthentikSignInPanel(app: WebPhoneApp, client: OkHttpClient, site: HttpUrl, location: HttpUrl?, onSignedIn: () -> Unit) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    var clientId by rememberSaveable { mutableStateOf(app.authentik.tokens()?.clientId ?: AuthentikSignIn.DEFAULT_CLIENT_ID) }
    var showClientId by rememberSaveable { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }

    Text(
        "This server is protected by authentik. Sign in with your authentik account first; the Web IP Phone sign-in comes after that.",
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
    if (showClientId) {
        OutlinedTextField(clientId, { clientId = it.trim(); error = null }, Modifier.fillMaxWidth(), label = { Text("authentik client ID") },
            singleLine = true, enabled = !busy)
    }
    ErrorText(error)
    Button(
        {
            busy = true
            error = null
            scope.launch {
                try {
                    AuthentikSignIn.signIn(ctx, app.authentik, client, site, location, clientId)
                    onSignedIn()
                } catch (e: CancellationException) {
                    if (!currentCoroutineContext().isActive) throw e
                } catch (e: IOException) {
                    error = e.message ?: "authentik sign-in failed."
                } catch (e: Exception) {
                    error = "authentik sign-in failed."
                } finally {
                    busy = false
                }
            }
        },
        Modifier.fillMaxWidth().height(52.dp).testTag("authentik"),
        enabled = !busy,
    ) {
        if (busy) CircularProgressIndicator(Modifier.size(22.dp), strokeWidth = 2.dp) else Text("Sign in with authentik")
    }
    if (busy) {
        TextButton({ AuthentikSignIn.cancel() }) { Text("Cancel") }
    } else {
        TextButton({ showClientId = !showClientId }) { Text(if (showClientId) "Hide client ID" else "authentik client ID…") }
    }
}

/** Full screen while signed in to Web IP Phone but the authentik sign-in is missing or ran out. */
@Composable
fun AuthentikScreen(app: WebPhoneApp) {
    val api = app.api
    val site = remember(api) { api?.baseUrl?.toHttpUrlOrNull() }
    Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
        Column(
            Modifier.fillMaxSize().systemBarsPadding().padding(24.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
        ) {
            Text("Sign in with authentik", style = MaterialTheme.typography.titleLarge)
            if (api != null && site != null) {
                Text(site.host, color = MaterialTheme.colorScheme.onSurfaceVariant)
                AuthentikSignInPanel(app, api.http, site, null) { app.authentikSignedIn() }
            }
            TextButton({ app.signOut() }) { Text("Sign out") }
        }
    }
}

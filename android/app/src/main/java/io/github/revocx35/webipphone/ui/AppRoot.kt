package io.github.revocx35.webipphone.ui

import android.Manifest
import android.os.Build
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import io.github.revocx35.webipphone.Session
import io.github.revocx35.webipphone.WebPhoneApp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

enum class Tab(val label: String) { KEYPAD("Keypad"), RECENTS("Recents"), SETTINGS("Settings") }

@Composable
fun AppRoot(app: WebPhoneApp) {
    val session by app.session.collectAsState()
    when (val s = session) {
        Session.SignedOut -> SignInFlow(app)
        is Session.Active -> {
            val me = s.me
            if (me != null && (me.mustChangePassword || me.mustEnroll2fa)) RestrictedFlow(app, me) else Home(app)
        }
    }
}

@Composable
fun Home(app: WebPhoneApp) {
    val st by app.phone.state.collectAsState()
    var tab by rememberSaveable { mutableStateOf(Tab.KEYPAD) }
    var number by rememberSaveable { mutableStateOf("") }
    val ctx = LocalContext.current
    val notifLauncher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {}
    LaunchedEffect(Unit) {
        if (Build.VERSION.SDK_INT >= 33 && !hasPermission(ctx, Manifest.permission.POST_NOTIFICATIONS)) {
            notifLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    LocalNetworkPrompt(app)

    val myCall = st.myCall
    val incoming = st.incoming
    if (incoming != null && myCall == null) {
        IncomingCallScreen(app, incoming.id, onAnswered = {}, onClose = {})
        return
    }
    if (myCall != null) {
        InCallScreen(app, myCall)
        return
    }
    BackHandler(tab != Tab.KEYPAD) { tab = Tab.KEYPAD }
    Scaffold(
        bottomBar = {
            NavigationBar {
                Tab.entries.forEach { t ->
                    NavigationBarItem(
                        selected = tab == t,
                        onClick = { tab = t },
                        icon = { Icon(t.icon(), contentDescription = null) },
                        label = { Text(t.label) },
                        modifier = Modifier.testTag("tab-" + t.name.lowercase()),
                    )
                }
            }
        },
    ) { padding ->
        Box(Modifier.padding(padding)) {
            when (tab) {
                Tab.KEYPAD -> KeypadScreen(app, number, { number = it })
                Tab.RECENTS -> RecentsScreen(app) { n -> number = n; tab = Tab.KEYPAD }
                Tab.SETTINGS -> SettingsScreen(app)
            }
        }
    }
}

/** Android 17+: asks again for local network access if the (LAN) server became unreachable
 *  because the permission is missing, e.g. revoked or after an upgrade to Android 17. */
@Composable
private fun LocalNetworkPrompt(app: WebPhoneApp) {
    if (Build.VERSION.SDK_INT < 37) return
    val ctx = LocalContext.current
    var show by remember { mutableStateOf(false) }
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { ok ->
        show = false
        if (ok) app.phone.onNetworkChanged()
    }
    LaunchedEffect(Unit) {
        val host = app.prefs.serverUrl?.let { android.net.Uri.parse(it).host } ?: return@LaunchedEffect
        show = !LocalNetwork.granted(ctx) && withContext(Dispatchers.IO) { LocalNetwork.isLan(host) }
    }
    if (show) {
        AlertDialog(
            onDismissRequest = { show = false },
            title = { Text("Allow local network access") },
            text = { Text("Your server is on your local network. Android needs your permission before the app can reach it.") },
            confirmButton = { TextButton({ launcher.launch(LocalNetwork.PERMISSION) }) { Text("Allow") } },
            dismissButton = { TextButton({ show = false }) { Text("Not now") } },
        )
    }
}

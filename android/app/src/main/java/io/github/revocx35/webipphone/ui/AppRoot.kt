package io.github.revocx35.webipphone.ui

import android.Manifest
import android.os.Build
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Icon
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import io.github.revocx35.webipphone.Session
import io.github.revocx35.webipphone.WebPhoneApp

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

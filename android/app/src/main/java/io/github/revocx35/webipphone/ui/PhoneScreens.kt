package io.github.revocx35.webipphone.ui

import android.Manifest
import android.content.Context
import android.os.PowerManager
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.systemBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.github.revocx35.webipphone.WebPhoneApp
import io.github.revocx35.webipphone.net.CallRecord
import io.github.revocx35.webipphone.net.CallView
import io.github.revocx35.webipphone.phone.ConnState
import io.github.revocx35.webipphone.phone.Route
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

private val KEYS = listOf("1" to "", "2" to "ABC", "3" to "DEF", "4" to "GHI", "5" to "JKL", "6" to "MNO",
    "7" to "PQRS", "8" to "TUV", "9" to "WXYZ", "*" to "", "0" to "+", "#" to "")
private val DIAL_RE = Regex("^[0-9A-Za-z*#+._-]{1,64}$")

@OptIn(ExperimentalFoundationApi::class)
@Composable
fun Keypad(onKey: (String) -> Unit, modifier: Modifier = Modifier) {
    Column(modifier.widthIn(max = 340.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        KEYS.chunked(3).forEach { row ->
            Row(horizontalArrangement = Arrangement.spacedBy(14.dp)) {
                row.forEach { (k, sub) ->
                    Surface(
                        shape = RoundedCornerShape(20.dp),
                        color = MaterialTheme.colorScheme.surfaceVariant,
                        modifier = Modifier.weight(1f).aspectRatio(1.4f).clip(RoundedCornerShape(20.dp))
                            .combinedClickable(onClick = { onKey(k) }, onLongClick = { if (k == "0") onKey("+") else onKey(k) })
                            .semantics { contentDescription = "key $k" },
                    ) {
                        Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
                            Text(k, fontSize = 26.sp)
                            Text(sub, fontSize = 10.sp, letterSpacing = 1.5.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                    }
                }
            }
        }
    }
}

@Composable
fun RoundButton(icon: ImageVector, color: Color, description: String, modifier: Modifier = Modifier, enabled: Boolean = true,
                rotate: Boolean = false, onClick: () -> Unit) {
    Surface(onClick = onClick, enabled = enabled, shape = CircleShape, color = if (enabled) color else color.copy(alpha = 0.4f),
        modifier = modifier.size(72.dp).semantics { contentDescription = description }) {
        Box(contentAlignment = Alignment.Center) {
            Icon(icon, null, tint = Color.White, modifier = Modifier.size(32.dp).then(if (rotate) Modifier.rotate(135f) else Modifier))
        }
    }
}

@Composable
private fun rememberMicPermission(onResult: (Boolean) -> Unit): () -> Unit {
    val ctx = LocalContext.current
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { onResult(it) }
    return { if (hasPermission(ctx, Manifest.permission.RECORD_AUDIO)) onResult(true) else launcher.launch(Manifest.permission.RECORD_AUDIO) }
}

@Composable
fun KeypadScreen(app: WebPhoneApp, number: String, setNumber: (String) -> Unit) {
    val st by app.phone.state.collectAsState()
    val scope = rememberCoroutineScope()
    var error by remember { mutableStateOf<String?>(null) }
    var menu by remember { mutableStateOf(false) }
    val dial: () -> Unit = {
        val n = number.filter { !it.isWhitespace() && it != '(' && it != ')' }
        if (DIAL_RE.matches(n)) scope.launch {
            error = null
            runCatching { app.phone.dial(n) }.onSuccess { setNumber("") }.onFailure { error = it.message }
        }
    }
    val askMic = rememberMicPermission { dial() }
    val phone = st.selectedPhone

    Column(Modifier.fillMaxSize().padding(horizontal = 20.dp), horizontalAlignment = Alignment.CenterHorizontally) {
        Spacer(Modifier.height(12.dp))
        if (st.conn != ConnState.ONLINE) {
            Text(if (st.conn == ConnState.CONNECTING) "Connecting to the server…" else "Offline",
                color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.labelLarge)
        }
        Box {
            Surface(onClick = { menu = true }, shape = RoundedCornerShape(12.dp), color = MaterialTheme.colorScheme.surfaceVariant,
                modifier = Modifier.fillMaxWidth().testTag("phone-picker")) {
                Row(Modifier.padding(horizontal = 14.dp, vertical = 12.dp), verticalAlignment = Alignment.CenterVertically) {
                    val (dot, label) = when {
                        phone == null -> Color.Gray to "No phone"
                        phone.reg.status == "registered" -> Color(0xFF16A34A) to "Registered"
                        phone.reg.status == "failed" -> Color(0xFFDC2626) to "Registration failed"
                        !phone.register -> Color.Gray to "Outgoing calls only"
                        else -> Color(0xFFD97706) to "Registering…"
                    }
                    Box(Modifier.size(10.dp).clip(CircleShape).background(dot))
                    Spacer(Modifier.width(10.dp))
                    Column(Modifier.weight(1f)) {
                        Text(phone?.let { "${it.title} · ${it.sipUser}" } ?: if (st.phones.isEmpty()) "No phone available" else "Choose a phone",
                            fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Text(phone?.let { "${it.pbxName} · $label" } ?: "Ask your administrator for access",
                            style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1)
                    }
                }
            }
            DropdownMenu(menu, { menu = false }) {
                st.phones.forEach { p ->
                    DropdownMenuItem(text = { Text("${p.title} · ${p.sipUser} @ ${p.pbxName}") }, onClick = { menu = false; app.phone.select(p.id) })
                }
            }
        }
        st.elsewhere?.let { c ->
            Surface(color = MaterialTheme.colorScheme.primaryContainer, shape = RoundedCornerShape(12.dp), modifier = Modifier.fillMaxWidth().padding(top = 10.dp)) {
                Row(Modifier.padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text("Call with ${c.who} is on another device", Modifier.weight(1f))
                    TextButton({ scope.launch { runCatching { app.phone.attach(c.id) }.onFailure { error = it.message } } }) { Text("Continue here") }
                }
            }
        }
        Spacer(Modifier.weight(1f))
        Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
            Text(number.ifEmpty { " " }, fontSize = 34.sp, textAlign = TextAlign.Center, maxLines = 1, modifier = Modifier.testTag("number"))
            if (number.isNotEmpty()) {
                IconButton({ setNumber(number.dropLast(1)) }, Modifier.align(Alignment.CenterEnd)) {
                    Icon(AppIcons.Backspace, "Delete")
                }
            }
        }
        ErrorText(error)
        Spacer(Modifier.height(12.dp))
        Keypad({ k ->
            app.phone.ringer.keyTone(k.first())
            if (number.length < 64) setNumber(number + k)
        })
        Spacer(Modifier.height(18.dp))
        RoundButton(AppIcons.PhoneFilled, CallGreen, "Call", enabled = number.isNotEmpty() && phone != null && st.conn == ConnState.ONLINE,
            modifier = Modifier.testTag("call"), onClick = askMic)
        Spacer(Modifier.height(20.dp))
    }
}

@Composable
fun RecentsScreen(app: WebPhoneApp, onPick: (String) -> Unit) {
    var calls by remember { mutableStateOf<List<CallRecord>?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    val st by app.phone.state.collectAsState()
    val ended = st.calls.count { it.ended }
    LaunchedEffect(ended) {
        delay(500)
        runCatching { app.api?.calls() }.onSuccess { calls = it; error = null }.onFailure { error = it.message }
    }
    Column(Modifier.fillMaxSize()) {
        Text("Recent calls", style = MaterialTheme.typography.titleLarge, modifier = Modifier.padding(20.dp))
        ErrorText(error)
        val list = calls
        when {
            list == null -> LinearProgressIndicator(Modifier.fillMaxWidth())
            list.isEmpty() -> Text("No calls yet", Modifier.fillMaxWidth().padding(40.dp), textAlign = TextAlign.Center,
                color = MaterialTheme.colorScheme.onSurfaceVariant)
            else -> LazyColumn {
                items(list, key = { it.id }) { c ->
                    val missed = c.direction == "in" && c.answeredAt == null
                    Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
                        Icon(if (c.direction == "in") AppIcons.ArrowIn else AppIcons.ArrowOut, null,
                            tint = if (missed) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.primary, modifier = Modifier.size(20.dp))
                        Spacer(Modifier.width(14.dp))
                        Column(Modifier.weight(1f)) {
                            Text(c.remoteName.ifBlank { c.remote }, fontWeight = FontWeight.SemiBold,
                                color = if (missed) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurface, maxLines = 1)
                            val dur = if (c.answeredAt != null && c.endedAt != null) {
                                val a = parseInstant(c.answeredAt); val e = parseInstant(c.endedAt)
                                if (a != null && e != null) formatDuration(java.time.Duration.between(a, e).seconds) else ""
                            } else c.status
                            Text(listOfNotNull(c.remote.takeIf { c.remoteName.isNotBlank() }, c.phoneLabel, dur).joinToString(" · "),
                                style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1)
                        }
                        Text(formatWhen(c.startedAt), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        IconButton({ onPick(c.remote) }) { Icon(AppIcons.Phone, "Call ${c.remote}", tint = MaterialTheme.colorScheme.primary) }
                    }
                    HorizontalDivider()
                }
            }
        }
    }
}

private fun statusText(c: CallView, now: Long): String = when (c.state) {
    "calling" -> "Calling…"
    "ringing" -> "Ringing…"
    "early" -> "Connecting…"
    "active" -> when {
        c.hold -> "On hold"
        c.remoteHold -> "Held by the other side"
        else -> formatDuration(secondsSince(c.answeredAt ?: c.startedAt) + 0 * now)
    }
    "ended" -> if (c.endStatus == "answered_elsewhere") "Answered on another device"
        else c.endReason?.takeIf { it != "hung up" && it != "remote hung up" }?.let { "Call ended: $it" } ?: "Call ended"
    else -> c.state
}

@Composable
private fun CallControl(icon: ImageVector, label: String, on: Boolean = false, enabled: Boolean = true, onClick: () -> Unit) {
    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        Surface(onClick = onClick, enabled = enabled, shape = CircleShape,
            color = if (on) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.surfaceVariant,
            modifier = Modifier.size(64.dp).semantics { contentDescription = label }) {
            Box(contentAlignment = Alignment.Center) {
                Icon(icon, null, tint = if (on) MaterialTheme.colorScheme.onPrimary else MaterialTheme.colorScheme.onSurface,
                    modifier = Modifier.size(26.dp))
            }
        }
        Text(label, style = MaterialTheme.typography.labelMedium, modifier = Modifier.padding(top = 6.dp),
            color = if (enabled) MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
fun InCallScreen(app: WebPhoneApp, call: CallView) {
    val scope = rememberCoroutineScope()
    val ctx = LocalContext.current
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    var muted by remember { mutableStateOf(app.phone.audio.muted) }
    var pad by remember { mutableStateOf(false) }
    var digits by remember { mutableStateOf("") }
    var transfer by remember { mutableStateOf<String?>(null) }
    var routeMenu by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    val routes by app.phone.router.routes.collectAsState()
    val route by app.phone.router.current.collectAsState()
    val level by app.phone.audio.level.collectAsState()
    val live = call.state == "active"
    LaunchedEffect(call.id) { while (true) { delay(500); now = System.currentTimeMillis() } }

    // Screen off while the phone is at the ear.
    DisposableEffect(route, live) {
        val pm = ctx.getSystemService(Context.POWER_SERVICE) as PowerManager
        val lock = if (route == Route.EARPIECE && pm.isWakeLockLevelSupported(PowerManager.PROXIMITY_SCREEN_OFF_WAKE_LOCK))
            pm.newWakeLock(PowerManager.PROXIMITY_SCREEN_OFF_WAKE_LOCK, "webphone:proximity").apply { acquire(4 * 60 * 60 * 1000L) } else null
        onDispose { lock?.takeIf { it.isHeld }?.release() }
    }
    fun run(block: suspend () -> Unit) = scope.launch { runCatching { block() }.onFailure { error = it.message } }

    Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
        Column(Modifier.fillMaxSize().systemBarsPadding().padding(24.dp), horizontalAlignment = Alignment.CenterHorizontally) {
            Spacer(Modifier.height(24.dp))
            Avatar(call.who)
            Spacer(Modifier.height(16.dp))
            Text(call.who, style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.SemiBold, maxLines = 1,
                overflow = TextOverflow.Ellipsis, modifier = Modifier.testTag("call-who"))
            if (call.remoteName.isNotBlank()) Text(call.remote, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(Modifier.height(8.dp))
            Text(statusText(call, now), color = if (live && !call.hold) CallGreen else MaterialTheme.colorScheme.onSurfaceVariant,
                fontWeight = FontWeight.Medium, modifier = Modifier.testTag("call-status"))
            if (live && !muted) {
                LinearProgressIndicator({ (kotlin.math.sqrt(level.toDouble()) * 2.6).toFloat().coerceIn(0f, 1f) },
                    Modifier.width(160.dp).padding(top = 10.dp))
            }
            ErrorText(error)
            Spacer(Modifier.weight(1f))
            if (pad && live) {
                Text(digits, fontSize = 24.sp, letterSpacing = 2.sp)
                Spacer(Modifier.height(10.dp))
                Keypad({ k -> digits = (digits + k).takeLast(24); run { app.phone.dtmf(call.id, k) } })
                TextButton({ pad = false }) { Text("Hide keypad") }
            } else {
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceEvenly) {
                    CallControl(if (muted) AppIcons.MicOff else AppIcons.Mic, if (muted) "Unmute" else "Mute", on = muted, enabled = !call.ended) {
                        muted = !muted
                        app.phone.audio.muted = muted
                    }
                    CallControl(AppIcons.Grid, "Keypad", enabled = live) { pad = true }
                    Box {
                        CallControl(AppIcons.Speaker, route.label, on = route == Route.SPEAKER, enabled = !call.ended) {
                            if (routes.size <= 2) {
                                app.phone.router.select(if (route == Route.SPEAKER) routes.firstOrNull { it != Route.SPEAKER } ?: Route.SPEAKER else Route.SPEAKER)
                            } else routeMenu = true
                        }
                        DropdownMenu(routeMenu, { routeMenu = false }) {
                            routes.forEach { r -> DropdownMenuItem(text = { Text(r.label) }, onClick = { routeMenu = false; app.phone.router.select(r) }) }
                        }
                    }
                }
                Spacer(Modifier.height(18.dp))
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceEvenly) {
                    CallControl(AppIcons.Pause, if (call.hold) "Resume" else "Hold", on = call.hold, enabled = live) { run { app.phone.hold(call.id, !call.hold) } }
                    CallControl(AppIcons.Transfer, "Transfer", enabled = live) { transfer = "" }
                }
            }
            Spacer(Modifier.height(28.dp))
            RoundButton(AppIcons.PhoneFilled, HangupRed, "Hang up", enabled = !call.ended, rotate = true, modifier = Modifier.testTag("hangup")) {
                run { app.phone.hangup(call.id) }
            }
            if (live && call.codec.isNotBlank()) {
                Text(if (call.codec == "PCMA") "G.711 A-law" else "G.711 µ-law", style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 10.dp))
            }
            Spacer(Modifier.height(12.dp))
        }
    }

    transfer?.let { t ->
        AlertDialog(
            onDismissRequest = { transfer = null },
            title = { Text("Transfer call") },
            text = { OutlinedTextField(t, { transfer = it }, label = { Text("Number") }, singleLine = true) },
            confirmButton = {
                TextButton({
                    val n = t.trim()
                    transfer = null
                    if (DIAL_RE.matches(n)) run { app.phone.transfer(call.id, n) }
                }) { Text("Transfer") }
            },
            dismissButton = { TextButton({ transfer = null }) { Text("Cancel") } },
        )
    }
}

@Composable
fun IncomingCallScreen(app: WebPhoneApp, callId: String, onAnswered: () -> Unit, onClose: () -> Unit) {
    val st by app.phone.state.collectAsState()
    val scope = rememberCoroutineScope()
    val call = st.calls.firstOrNull { it.id == callId }
    var error by remember { mutableStateOf<String?>(null) }
    val answer: (Boolean) -> Unit = { _ ->
        scope.launch {
            runCatching { app.phone.answer(callId) }.onSuccess { onAnswered() }.onFailure { error = it.message }
        }
    }
    val askMic = rememberMicPermission(answer)
    val line = call?.let { c -> st.phones.firstOrNull { it.id == c.phoneId } }

    Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
        Column(Modifier.fillMaxSize().systemBarsPadding().padding(24.dp), horizontalAlignment = Alignment.CenterHorizontally) {
            Spacer(Modifier.height(40.dp))
            Text("Incoming call" + (line?.let { " · ${it.title}" } ?: ""), color = MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(Modifier.height(28.dp))
            Avatar(call?.who ?: "", 110.dp)
            Spacer(Modifier.height(18.dp))
            Text(call?.who ?: "", style = MaterialTheme.typography.headlineMedium, fontWeight = FontWeight.SemiBold, maxLines = 1,
                overflow = TextOverflow.Ellipsis, modifier = Modifier.testTag("incoming-who"))
            if (call?.remoteName?.isNotBlank() == true) Text(call.remote, color = MaterialTheme.colorScheme.onSurfaceVariant)
            ErrorText(error)
            Spacer(Modifier.weight(1f))
            Row(Modifier.fillMaxWidth().padding(bottom = 40.dp), horizontalArrangement = Arrangement.SpaceEvenly) {
                Column(horizontalAlignment = Alignment.CenterHorizontally) {
                    RoundButton(AppIcons.PhoneFilled, HangupRed, "Decline", rotate = true, modifier = Modifier.testTag("decline")) {
                        scope.launch { runCatching { app.phone.hangup(callId) }; onClose() }
                    }
                    Text("Decline", Modifier.padding(top = 8.dp))
                }
                Column(horizontalAlignment = Alignment.CenterHorizontally) {
                    RoundButton(AppIcons.PhoneFilled, CallGreen, "Answer", modifier = Modifier.testTag("answer"), onClick = askMic)
                    Text("Answer", Modifier.padding(top = 8.dp))
                }
            }
        }
    }
}

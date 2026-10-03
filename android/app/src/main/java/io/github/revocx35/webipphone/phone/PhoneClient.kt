package io.github.revocx35.webipphone.phone

import android.content.BroadcastReceiver
import android.content.Intent
import android.util.Log
import io.github.revocx35.webipphone.IncomingCallActivity
import io.github.revocx35.webipphone.WebPhoneApp
import io.github.revocx35.webipphone.net.CallView
import io.github.revocx35.webipphone.net.PhoneView
import io.github.revocx35.webipphone.net.json
import kotlin.math.min
import kotlin.random.Random
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import okio.ByteString.Companion.toByteString

enum class ConnState { OFFLINE, CONNECTING, ONLINE }

data class PhoneState(
    val conn: ConnState = ConnState.OFFLINE,
    val phones: List<PhoneView> = emptyList(),
    val calls: List<CallView> = emptyList(),
    val selected: Long = 0,
    val serverVersion: String = "",
) {
    /** The call whose audio runs on this device. */
    val myCall: CallView? get() = calls.firstOrNull { it.mine && it.attached }
    /** An incoming call offered to this device. */
    val incoming: CallView? get() = calls.firstOrNull { it.state == "incoming" && !it.mine }
    /** A call of this user that runs on another device. */
    val elsewhere: CallView? get() = calls.firstOrNull { it.mine && !it.attached && !it.ended && it.state != "incoming" }
    val selectedPhone: PhoneView? get() = phones.firstOrNull { it.id == selected }
}

class CommandException(message: String) : Exception(message)

/** The WebSocket session with the server and the call logic on the phone. */
class PhoneClient(private val app: WebPhoneApp) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    val state = MutableStateFlow(PhoneState(selected = app.prefs.selectedPhone))
    val ringer = Ringer(app)
    val router = AudioRouter(app)
    val audio = CallAudio(app) { frame -> ws?.send(frame.toByteString()) ?: false }
    /** The phone book (own + shared contacts); names callers on every screen and notification. */
    val contacts = MutableStateFlow(ContactIndex.EMPTY)
    private var contactsJob: Job? = null
    private var contactsAgain = false

    private var ws: WebSocket? = null
    private var wanted = false
    private var retry = 0
    private var retryJob: Job? = null
    private var seq = 0
    private val pending = HashMap<String, CompletableDeferred<String?>>()
    private var audioCall: String? = null
    /** Call to re-attach to after a reconnect (network switch). */
    private var resumeCall: String? = null
    private val ringingFor = HashSet<String>()
    private var ringbackFor: String? = null

    /** Called when the server ended the session (signed out / disabled). */
    var onSessionEnded: ((String) -> Unit)? = null

    fun start() {
        wanted = true
        if (ws == null && retryJob?.isActive != true) connect()
    }

    fun stop() {
        wanted = false
        retryJob?.cancel()
        ws?.close(1000, "bye")
        ws = null
        failPending("Disconnected")
        stopCallAudio()
        ringer.stopRinging()
        ringer.stopRingback()
        Notifications.cancelIncoming(app)
        state.update { it.copy(conn = ConnState.OFFLINE, calls = emptyList()) }
    }

    /** A new network appeared: a socket on the old one may be dead; reconnect at once. */
    fun onNetworkChanged() {
        if (!wanted) return
        if (state.value.conn != ConnState.ONLINE || audioCall != null) {
            Log.i(TAG, "network changed: reconnecting")
            ws?.cancel()
            ws = null
            retryJob?.cancel()
            connect()
        }
    }

    private fun connect() {
        val api = app.api ?: return
        state.update { it.copy(conn = ConnState.CONNECTING) }
        val socket = api.webSocket(object : WebSocketListener() {
            override fun onMessage(webSocket: WebSocket, text: String) {
                scope.launch { if (ws === webSocket) handle(text) }
            }

            override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
                if (bytes.size > 1 && bytes[0] == CallAudio.FRAME_AUDIO && audioCall != null) {
                    audio.play(bytes.substring(1).toByteArray())
                }
            }

            override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                webSocket.close(1000, null)
                scope.launch { closed(webSocket, code, reason) }
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                val code = response?.code ?: 0
                scope.launch { closed(webSocket, if (code == 401) 4401 else 0, t.message ?: "") }
            }
        })
        ws = socket
    }

    private fun closed(socket: WebSocket, code: Int, reason: String) {
        if (ws !== socket) return
        ws = null
        failPending("Connection lost")
        if (code == 1008 || code == 4401) {
            // Session revoked, signed out elsewhere, or disabled.
            stop()
            onSessionEnded?.invoke(reason.ifBlank { "Your session ended" })
            return
        }
        state.update { it.copy(conn = ConnState.CONNECTING) }
        if (!wanted) return
        val delayMs = (min(15_000.0, 500.0 * (1 shl min(retry++, 5))) * (0.75 + Random.nextDouble() * 0.5)).toLong()
        retryJob = scope.launch {
            delay(delayMs)
            if (wanted && ws == null) connect()
        }
    }

    private fun failPending(msg: String) {
        pending.values.forEach { it.completeExceptionally(CommandException(msg)) }
        pending.clear()
    }

    private fun handle(text: String) {
        val obj = runCatching { json.parseToJsonElement(text).jsonObject }.getOrNull() ?: return
        when (obj["type"]?.jsonPrimitive?.content) {
            "hello" -> {
                retry = 0
                val phones = obj.list("phones", PhoneView.serializer())
                val calls = obj.list("calls", CallView.serializer()).map(::named)
                var selected = state.value.selected
                if (phones.none { it.id == selected }) selected = phones.firstOrNull()?.id ?: 0
                state.update { it.copy(conn = ConnState.ONLINE, phones = phones, calls = calls, selected = selected,
                    serverVersion = obj["version"]?.jsonPrimitive?.content ?: "") }
                goOnline(selected)
                resumeCall?.let { id ->
                    if (calls.any { it.id == id && it.mine && !it.ended }) send(mapOf("type" to "attach", "call" to id))
                }
                reconcile()
                reloadContacts() // also catches changes made while this connection was down
            }
            "phones" -> {
                val phones = obj.list("phones", PhoneView.serializer())
                val prev = state.value.selected
                var selected = prev
                if (phones.none { it.id == selected }) selected = phones.firstOrNull()?.id ?: 0
                state.update { it.copy(phones = phones, selected = selected) }
                if (selected != prev || phones.firstOrNull { it.id == selected }?.online == false) goOnline(selected)
            }
            "call" -> {
                val call = runCatching { json.decodeFromJsonElement(CallView.serializer(), obj["call"]!!) }.getOrNull() ?: return
                upsert(named(call))
            }
            "contacts" -> reloadContacts()
            "ack" -> pending.remove(obj["req"]?.jsonPrimitive?.content)?.complete((obj["call"] as? JsonPrimitive)?.content)
            "error" -> {
                val req = obj["req"]?.jsonPrimitive?.content
                val msg = obj["error"]?.jsonPrimitive?.content ?: "Error"
                pending.remove(req)?.completeExceptionally(CommandException(msg)) ?: Log.w(TAG, "server: $msg")
            }
        }
    }

    /** Reloads the phone book; requests while a load runs are merged into one more load. */
    fun reloadContacts() {
        val api = app.api ?: return
        if (contactsJob?.isActive == true) {
            contactsAgain = true
            return
        }
        contactsJob = scope.launch {
            do {
                contactsAgain = false
                runCatching { api.contacts() }
                    .onSuccess { list ->
                        contacts.value = ContactIndex(list)
                        state.update { s -> s.copy(calls = s.calls.map(::named)) }
                    }
                    .onFailure { Log.w(TAG, "contacts: ${it.message}") }
            } while (contactsAgain)
        }
    }

    /** Forgets the phone book (sign-out). */
    fun clearContacts() {
        contactsJob?.cancel()
        contacts.value = ContactIndex.EMPTY
    }

    private fun named(c: CallView): CallView {
        val hit = contacts.value.lookup(c.remote)
        return c.copy(contactName = hit?.contact?.name ?: "", contactLabel = hit?.number?.label ?: "")
    }

    private fun <T> JsonObject.list(key: String, s: kotlinx.serialization.KSerializer<T>): List<T> =
        this[key]?.let { runCatching { json.decodeFromJsonElement(ListSerializer(s), it) }.getOrNull() } ?: emptyList()

    private fun upsert(c: CallView) {
        val before = state.value.calls.firstOrNull { it.id == c.id }
        state.update { s -> s.copy(calls = s.calls.filter { it.id != c.id } + c) }
        if (c.ended) {
            if (before != null && !before.mine && before.state == "incoming" && c.endStatus == "missed") Notifications.missed(app, c)
            if (resumeCall == c.id) resumeCall = null
            scope.launch {
                delay(2500)
                state.update { s -> s.copy(calls = s.calls.filter { it.id != c.id || !it.ended }) }
            }
        }
        reconcile()
    }

    /** Aligns audio, ringing and the service with the call states. */
    private fun reconcile() {
        val s = state.value
        val mine = s.calls.firstOrNull { it.mine && it.attached && !it.ended }
        // audio
        if (mine != null && (mine.state == "active" || mine.state == "early")) {
            if (audioCall != mine.id) {
                audioCall = mine.id
                resumeCall = mine.id
                router.begin()
                audio.start(mine.codec)
            } else {
                audio.setCodec(mine.codec)
            }
        } else if (audioCall != null && (mine == null || mine.id != audioCall)) {
            stopCallAudio()
        }
        if (mine != null && mine.state in setOf("calling", "ringing")) router.begin()
        if (mine == null && audioCall == null) router.end()
        // ringback while the far end rings without early media
        val rb = mine?.takeIf { it.state == "ringing" }?.id
        if (rb != ringbackFor) {
            ringbackFor = rb
            if (rb != null) ringer.startRingback() else ringer.stopRingback()
        }
        // incoming
        val incoming = s.incoming
        if (incoming != null && !incoming.ended && mine == null) {
            if (ringingFor.add(incoming.id)) {
                ringer.ring()
                // In the foreground the app shows its own incoming-call screen.
                if (!app.inForeground) showIncomingNotification(incoming)
            }
        } else if (ringingFor.isNotEmpty()) {
            ringingFor.clear()
            ringer.stopRinging()
            Notifications.cancelIncoming(app)
        }
        // keep the process alive during calls
        if (mine != null) PhoneService.start(app)
    }

    private fun showIncomingNotification(c: CallView) {
        val line = state.value.phones.firstOrNull { it.id == c.phoneId }?.let { "${it.title} (${it.sipUser})" } ?: ""
        Notifications.showIncoming(app, c, line)
        // Full-screen notifications blocked: open the call screen ourselves if we may.
        if (!CallScreenAccess.fullScreenAllowed(app)) launchCallScreen(c)
    }

    private fun launchCallScreen(c: CallView) {
        if (app.inForeground || IncomingCallActivity.shownCall == c.id || !CallScreenAccess.overlayAllowed(app)) return
        runCatching {
            app.startActivity(Intent(app, IncomingCallActivity::class.java)
                .putExtra(IncomingCallActivity.EXTRA_CALL, c.id).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        }.onFailure { Log.w(TAG, "cannot open the call screen", it) }
    }

    /** The screen was turned on or unlocked: show a ringing call's screen if it is not up yet. */
    fun onScreenOn() {
        val c = state.value.incoming?.takeIf { it.id in ringingFor } ?: return
        launchCallScreen(c)
    }

    /** The app moved to/from the foreground while a call may be ringing. */
    fun onForegroundChanged(foreground: Boolean) {
        val ringing = state.value.incoming?.takeIf { it.id in ringingFor } ?: return
        if (foreground) Notifications.cancelIncoming(app) else showIncomingNotification(ringing)
    }

    private fun stopCallAudio() {
        audioCall = null
        audio.stop()
        router.end()
    }

    private fun send(m: Map<String, Any?>): Boolean {
        val obj = JsonObject(m.mapValues { (_, v) ->
            when (v) {
                is String -> JsonPrimitive(v)
                is Number -> JsonPrimitive(v)
                is Boolean -> JsonPrimitive(v)
                is List<*> -> kotlinx.serialization.json.JsonArray(v.map { JsonPrimitive(it as Number) })
                else -> kotlinx.serialization.json.JsonNull
            }
        })
        return ws?.send(json.encodeToString(JsonElement.serializer(), obj)) ?: false
    }

    private suspend fun request(m: Map<String, Any?>): String? {
        val req = "a${++seq}"
        val d = CompletableDeferred<String?>()
        pending[req] = d
        if (!send(m + ("req" to req))) {
            pending.remove(req)
            throw CommandException("Not connected to the server")
        }
        return try {
            withTimeout(20_000) { d.await() }
        } catch (e: kotlinx.coroutines.TimeoutCancellationException) {
            pending.remove(req)
            throw CommandException("The server did not answer")
        }
    }

    private fun goOnline(phoneId: Long) {
        send(mapOf("type" to "online", "phones" to if (phoneId > 0) listOf(phoneId) else emptyList<Long>()))
    }

    fun select(phoneId: Long) {
        app.prefs.selectedPhone = phoneId
        state.update { it.copy(selected = phoneId) }
        goOnline(phoneId)
    }

    suspend fun dial(number: String): String? {
        val phone = state.value.selected
        if (phone <= 0) throw CommandException("Choose a phone first")
        audio.muted = false
        return request(mapOf("type" to "dial", "phone" to phone, "number" to number))
    }

    suspend fun answer(id: String) {
        audio.muted = false
        state.value.myCall?.takeIf { it.id != id && !it.ended }?.let { runCatching { hangup(it.id) } }
        request(mapOf("type" to "answer", "call" to id))
    }

    suspend fun hangup(id: String) { request(mapOf("type" to "hangup", "call" to id)) }

    suspend fun dtmf(id: String, digits: String) {
        digits.firstOrNull()?.let { ringer.keyTone(it) }
        request(mapOf("type" to "dtmf", "call" to id, "digits" to digits))
    }

    suspend fun hold(id: String, on: Boolean) { request(mapOf("type" to "hold", "call" to id, "on" to on)) }

    suspend fun transfer(id: String, number: String) { request(mapOf("type" to "transfer", "call" to id, "number" to number)) }

    suspend fun attach(id: String) { request(mapOf("type" to "attach", "call" to id)) }

    /** Runs a command from a broadcast receiver. */
    fun launchCommand(pending: BroadcastReceiver.PendingResult, block: suspend () -> Unit) {
        scope.launch {
            try {
                if (state.value.conn != ConnState.ONLINE) start()
                block()
            } catch (e: Exception) {
                Log.w(TAG, "command failed", e)
            } finally {
                pending.finish()
            }
        }
    }

    companion object {
        private const val TAG = "PhoneClient"
    }
}

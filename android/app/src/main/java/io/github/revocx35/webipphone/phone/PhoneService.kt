package io.github.revocx35.webipphone.phone

import android.Manifest
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import android.util.Log
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import io.github.revocx35.webipphone.WebPhoneApp
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.launch

/**
 * Foreground service that keeps the process (and its WebSocket) alive: while a call is in
 * progress (type phoneCall|microphone) and, if enabled, while waiting for calls (specialUse).
 */
class PhoneService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private var job: Job? = null
    private var lastType = -1

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val app = application as WebPhoneApp
        // startForeground must follow startForegroundService promptly.
        update(app, force = true)
        if (job == null) {
            job = scope.launch {
                app.phone.state.collectLatest { update(app, force = false) }
            }
        }
        app.phone.start()
        return START_STICKY
    }

    private fun update(app: WebPhoneApp, force: Boolean) {
        val st = app.phone.state.value
        val call = st.calls.firstOrNull { it.mine && it.attached && !it.ended }
        if (call == null && !app.prefs.backgroundCalls) {
            if (!force) {
                ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
                stopSelf()
                return
            }
        }
        val mic = ContextCompat.checkSelfPermission(this, Manifest.permission.RECORD_AUDIO) == PackageManager.PERMISSION_GRANTED
        val type = when {
            Build.VERSION.SDK_INT < 29 -> 0
            call != null -> ServiceInfo.FOREGROUND_SERVICE_TYPE_PHONE_CALL or (if (mic) ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE else 0)
            Build.VERSION.SDK_INT >= 34 -> ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE
            else -> ServiceInfo.FOREGROUND_SERVICE_TYPE_PHONE_CALL
        }
        val notification = if (call != null) Notifications.ongoing(this, call) else {
            val phone = st.phones.firstOrNull { it.id == st.selected }
            val text = when {
                st.conn != ConnState.ONLINE -> "Connecting to the server…"
                phone == null -> "No phone selected"
                phone.reg.status == "registered" -> "Ready for calls on ${phone.title} (${phone.sipUser})"
                phone.register -> "Registering ${phone.sipUser}…"
                else -> "${phone.title}: outgoing calls only"
            }
            Notifications.ready(this, text)
        }
        try {
            if (force || type != lastType) {
                ServiceCompat.startForeground(this, Notifications.ID_SERVICE, notification, type)
                lastType = type
            } else {
                getSystemService(android.app.NotificationManager::class.java).notify(Notifications.ID_SERVICE, notification)
            }
        } catch (e: Exception) {
            // e.g. microphone type while the app is in the background: keep the call type only.
            Log.w("PhoneService", "startForeground failed", e)
            if (call != null && Build.VERSION.SDK_INT >= 29) {
                runCatching { ServiceCompat.startForeground(this, Notifications.ID_SERVICE, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_PHONE_CALL) }
            }
        }
    }

    override fun onDestroy() {
        scope.cancel()
        super.onDestroy()
    }

    companion object {
        fun start(ctx: Context) {
            runCatching { ContextCompat.startForegroundService(ctx, Intent(ctx, PhoneService::class.java)) }
                .onFailure { Log.w("PhoneService", "cannot start", it) }
        }

        fun stop(ctx: Context) {
            ctx.stopService(Intent(ctx, PhoneService::class.java))
        }
    }
}

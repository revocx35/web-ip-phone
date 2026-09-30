package io.github.revocx35.webipphone

import android.app.Application
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.ProcessLifecycleOwner
import io.github.revocx35.webipphone.data.Prefs
import io.github.revocx35.webipphone.net.Api
import io.github.revocx35.webipphone.net.ApiException
import io.github.revocx35.webipphone.net.Me
import io.github.revocx35.webipphone.phone.PhoneClient
import io.github.revocx35.webipphone.phone.PhoneService
import io.github.revocx35.webipphone.phone.Notifications
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch

sealed interface Session {
    data object SignedOut : Session
    /** Signed in; [me] is null until the server was reached. */
    data class Active(val me: Me?) : Session
}

class WebPhoneApp : Application() {
    lateinit var prefs: Prefs
        private set
    @Volatile var api: Api? = null
        private set
    lateinit var phone: PhoneClient
        private set
    val session = MutableStateFlow<Session>(Session.SignedOut)
    /** Message shown on the sign-in screen after the server ended the session. */
    val signOutReason = MutableStateFlow<String?>(null)
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private var stopJob: Job? = null
    @Volatile var inForeground = false
        private set

    val signedIn get() = api?.token != null

    override fun onCreate() {
        super.onCreate()
        prefs = Prefs(this)
        Notifications.createChannels(this)
        val url = prefs.serverUrl
        val token = prefs.token
        if (url != null && token != null) {
            api = Api(url, prefs.pinnedCert, token)
            session.value = Session.Active(null)
        }
        phone = PhoneClient(this)
        phone.onSessionEnded = { reason -> signOutLocal(reason) }

        ProcessLifecycleOwner.get().lifecycle.addObserver(object : DefaultLifecycleObserver {
            override fun onStart(owner: LifecycleOwner) {
                inForeground = true
                phone.onForegroundChanged(true)
                stopJob?.cancel()
                if (signedIn) {
                    phone.start()
                    refreshMe()
                    if (prefs.backgroundCalls) PhoneService.start(this@WebPhoneApp)
                }
            }

            override fun onStop(owner: LifecycleOwner) {
                inForeground = false
                phone.onForegroundChanged(false)
                if (!prefs.backgroundCalls) {
                    stopJob = scope.launch {
                        delay(20_000)
                        if (!inForeground && phone.state.value.myCall == null) phone.stop()
                    }
                }
            }
        })

        getSystemService(ConnectivityManager::class.java).registerNetworkCallback(
            NetworkRequest.Builder().addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET).build(),
            object : ConnectivityManager.NetworkCallback() {
                override fun onAvailable(network: Network) {
                    scope.launch { phone.onNetworkChanged() }
                }
            },
        )
    }

    fun refreshMe() {
        val a = api ?: return
        scope.launch {
            try {
                session.value = Session.Active(a.me())
            } catch (e: ApiException) {
                if (e.status == 401) signOutLocal("Your session ended. Sign in again.")
            } catch (_: Exception) {
                // offline: keep the session
            }
        }
    }

    fun completeLogin(url: String, pin: String?, token: String, me: Me) {
        prefs.serverUrl = url
        prefs.pinnedCert = pin
        prefs.token = token
        prefs.username = me.username
        signOutReason.value = null
        api = Api(url, pin, token)
        session.value = Session.Active(me)
        phone.start()
        if (prefs.backgroundCalls) PhoneService.start(this)
    }

    fun setBackgroundCalls(on: Boolean) {
        prefs.backgroundCalls = on
        if (on && signedIn) PhoneService.start(this) else if (phone.state.value.myCall == null) PhoneService.stop(this)
    }

    /** Signs out on the server, then locally. */
    fun signOut() {
        val a = api
        scope.launch { a?.logout() }
        signOutLocal(null)
    }

    fun signOutLocal(reason: String?) {
        phone.stop()
        PhoneService.stop(this)
        prefs.signOut()
        api = null
        signOutReason.value = reason
        session.value = Session.SignedOut
    }
}

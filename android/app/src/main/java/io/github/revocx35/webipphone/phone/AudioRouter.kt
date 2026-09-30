package io.github.revocx35.webipphone.phone

import android.content.Context
import android.media.AudioAttributes
import android.media.AudioDeviceCallback
import android.media.AudioDeviceInfo
import android.media.AudioFocusRequest
import android.media.AudioManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import kotlinx.coroutines.flow.MutableStateFlow

enum class Route(val label: String) { EARPIECE("Phone"), SPEAKER("Speaker"), BLUETOOTH("Bluetooth"), WIRED("Headset") }

/** Call audio mode, focus and output selection (earpiece / speaker / Bluetooth / wired). */
class AudioRouter(context: Context) {
    private val am = context.getSystemService(AudioManager::class.java)
    private val handler = Handler(Looper.getMainLooper())
    private var focus: AudioFocusRequest? = null
    private var active = false
    private var userChoice: Route? = null

    val routes = MutableStateFlow<List<Route>>(emptyList())
    val current = MutableStateFlow(Route.EARPIECE)

    private val deviceCallback = object : AudioDeviceCallback() {
        override fun onAudioDevicesAdded(added: Array<out AudioDeviceInfo>) = refresh(autoSwitch = true)
        override fun onAudioDevicesRemoved(removed: Array<out AudioDeviceInfo>) = refresh(autoSwitch = true)
    }

    fun begin() {
        if (active) return
        active = true
        userChoice = null
        val attrs = AudioAttributes.Builder().setUsage(AudioAttributes.USAGE_VOICE_COMMUNICATION)
            .setContentType(AudioAttributes.CONTENT_TYPE_SPEECH).build()
        focus = AudioFocusRequest.Builder(AudioManager.AUDIOFOCUS_GAIN_TRANSIENT).setAudioAttributes(attrs).build()
            .also { am.requestAudioFocus(it) }
        am.mode = AudioManager.MODE_IN_COMMUNICATION
        am.registerAudioDeviceCallback(deviceCallback, handler)
        refresh(autoSwitch = true)
    }

    fun end() {
        if (!active) return
        active = false
        am.unregisterAudioDeviceCallback(deviceCallback)
        if (Build.VERSION.SDK_INT >= 31) {
            am.clearCommunicationDevice()
        } else {
            @Suppress("DEPRECATION")
            am.isSpeakerphoneOn = false
            @Suppress("DEPRECATION")
            if (am.isBluetoothScoOn) { am.stopBluetoothSco(); am.isBluetoothScoOn = false }
        }
        am.mode = AudioManager.MODE_NORMAL
        focus?.let { am.abandonAudioFocusRequest(it) }
        focus = null
    }

    fun select(route: Route) {
        userChoice = route
        apply(route)
    }

    private fun available(): Map<Route, AudioDeviceInfo?> {
        val out = LinkedHashMap<Route, AudioDeviceInfo?>()
        val devices = if (Build.VERSION.SDK_INT >= 31) am.availableCommunicationDevices
        else am.getDevices(AudioManager.GET_DEVICES_OUTPUTS).toList()
        for (d in devices) {
            val r = when (d.type) {
                AudioDeviceInfo.TYPE_BUILTIN_EARPIECE -> Route.EARPIECE
                AudioDeviceInfo.TYPE_BUILTIN_SPEAKER -> Route.SPEAKER
                AudioDeviceInfo.TYPE_BLUETOOTH_SCO, AudioDeviceInfo.TYPE_BLE_HEADSET -> Route.BLUETOOTH
                AudioDeviceInfo.TYPE_WIRED_HEADSET, AudioDeviceInfo.TYPE_WIRED_HEADPHONES, AudioDeviceInfo.TYPE_USB_HEADSET -> Route.WIRED
                else -> null
            } ?: continue
            if (!out.containsKey(r)) out[r] = d
        }
        if (out.isEmpty()) out[Route.SPEAKER] = null
        return out
    }

    private fun refresh(autoSwitch: Boolean) {
        if (!active) return
        val av = available()
        routes.value = Route.entries.filter { av.containsKey(it) }
        val choice = userChoice?.takeIf { av.containsKey(it) }
        val best = choice ?: when {
            av.containsKey(Route.BLUETOOTH) -> Route.BLUETOOTH
            av.containsKey(Route.WIRED) -> Route.WIRED
            av.containsKey(Route.EARPIECE) -> Route.EARPIECE
            else -> Route.SPEAKER
        }
        if (autoSwitch || best != current.value) apply(best)
    }

    private fun apply(route: Route) {
        if (!active) return
        if (Build.VERSION.SDK_INT >= 31) {
            val dev = available()[route]
            if (dev != null) am.setCommunicationDevice(dev) else am.clearCommunicationDevice()
        } else {
            @Suppress("DEPRECATION")
            when (route) {
                Route.SPEAKER -> { stopSco(); am.isSpeakerphoneOn = true }
                Route.BLUETOOTH -> { am.isSpeakerphoneOn = false; am.startBluetoothSco(); am.isBluetoothScoOn = true }
                else -> { stopSco(); am.isSpeakerphoneOn = false }
            }
        }
        current.value = route
    }

    @Suppress("DEPRECATION")
    private fun stopSco() {
        if (am.isBluetoothScoOn) {
            am.stopBluetoothSco()
            am.isBluetoothScoOn = false
        }
    }
}

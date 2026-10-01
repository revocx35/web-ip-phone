package io.github.revocx35.webipphone.data

import android.content.Context
import androidx.core.content.edit

/** App settings and the (encrypted) session. One server account at a time. */
class Prefs(context: Context, private val cipher: SecretCipher = KeystoreCipher()) {
    private val sp = context.getSharedPreferences("webphone", Context.MODE_PRIVATE)

    /** Normalized base URL, e.g. https://phone.example.com or https://192.168.1.5:8443 */
    var serverUrl: String?
        get() = sp.getString("server", null)
        set(v) = sp.edit { putString("server", v) }

    /** SHA-256 fingerprint of a self-signed server certificate the user chose to trust. */
    var pinnedCert: String?
        get() = sp.getString("pin", null)
        set(v) = sp.edit { putString("pin", v) }

    var username: String?
        get() = sp.getString("user", null)
        set(v) = sp.edit { putString("user", v) }

    var token: String?
        get() = sp.getString("token", null)?.let { cipher.decrypt(it) }
        set(v) = sp.edit { if (v == null) remove("token") else putString("token", cipher.encrypt(v)) }

    var selectedPhone: Long
        get() = sp.getLong("phone", 0)
        set(v) = sp.edit { putLong("phone", v) }

    /** Keep the connection (and a notification) while the app is closed, to receive calls. */
    var backgroundCalls: Boolean
        get() = sp.getBoolean("background", true)
        set(v) = sp.edit { putBoolean("background", v) }

    var askedBattery: Boolean
        get() = sp.getBoolean("askedBattery", false)
        set(v) = sp.edit { putBoolean("askedBattery", v) }

    /** The user saw the explanation about showing calls on the lock screen. */
    var askedCallScreen: Boolean
        get() = sp.getBoolean("askedCallScreen", false)
        set(v) = sp.edit { putBoolean("askedCallScreen", v) }

    fun signOut() = sp.edit { remove("token") }

    fun forgetServer() = sp.edit {
        remove("token"); remove("server"); remove("pin"); remove("phone")
    }
}

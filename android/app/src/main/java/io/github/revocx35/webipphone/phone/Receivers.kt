package io.github.revocx35.webipphone.phone

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import io.github.revocx35.webipphone.WebPhoneApp

/** Decline / hang up buttons of the call notifications. */
class CallActionReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val app = context.applicationContext as WebPhoneApp
        val id = intent.getStringExtra(EXTRA_CALL) ?: return
        val pending = goAsync()
        app.phone.launchCommand(pending) {
            when (intent.action) {
                ACTION_DECLINE -> app.phone.hangup(id)
                ACTION_HANGUP -> app.phone.hangup(id)
            }
        }
    }

    companion object {
        const val ACTION_DECLINE = "io.github.revocx35.webipphone.DECLINE"
        const val ACTION_HANGUP = "io.github.revocx35.webipphone.HANGUP"
        const val EXTRA_CALL = "call"
    }
}

/** Restarts the background connection after a reboot or an app update, if enabled. */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != Intent.ACTION_BOOT_COMPLETED && intent.action != Intent.ACTION_MY_PACKAGE_REPLACED) return
        val app = context.applicationContext as WebPhoneApp
        if (app.signedIn && app.prefs.backgroundCalls) PhoneService.start(context)
    }
}

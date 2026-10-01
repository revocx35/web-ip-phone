package io.github.revocx35.webipphone

import android.content.Intent
import android.os.Build
import android.os.Bundle
import android.view.WindowManager
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.lifecycle.lifecycleScope
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.testTagsAsResourceId
import io.github.revocx35.webipphone.ui.IncomingCallScreen
import io.github.revocx35.webipphone.ui.WebPhoneTheme
import kotlinx.coroutines.launch

/** Full-screen incoming call UI (also over the lock screen). Answering hands over to MainActivity. */
class IncomingCallActivity : ComponentActivity() {
    private val app get() = application as WebPhoneApp

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        if (Build.VERSION.SDK_INT >= 27) {
            setShowWhenLocked(true)
            setTurnScreenOn(true)
        } else {
            @Suppress("DEPRECATION")
            window.addFlags(WindowManager.LayoutParams.FLAG_SHOW_WHEN_LOCKED or WindowManager.LayoutParams.FLAG_TURN_SCREEN_ON)
        }
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        setContent {
            WebPhoneTheme {
                androidx.compose.foundation.layout.Box(androidx.compose.ui.Modifier.semantics { testTagsAsResourceId = true }) {
                    IncomingCallScreen(app, callId(), onAnswered = ::openInCall, onClose = ::finish)
                }
            }
        }
        if (intent.getBooleanExtra(EXTRA_ANSWER, false)) answer()
        // Close when the call stops ringing here (answered elsewhere, caller hung up).
        lifecycleScope.launch {
            app.phone.state.collect { st ->
                val id = callId()
                if (st.calls.none { it.id == id && it.state == "incoming" } && st.myCall?.id != id) finish()
            }
        }
    }

    override fun onStart() {
        super.onStart()
        shownCall = callId()
    }

    override fun onStop() {
        if (shownCall == callId()) shownCall = null
        super.onStop()
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        if (intent.getBooleanExtra(EXTRA_ANSWER, false)) answer()
    }

    private fun callId() = intent.getStringExtra(EXTRA_CALL) ?: ""

    private fun answer() {
        lifecycleScope.launch {
            runCatching { app.phone.answer(callId()) }
            openInCall()
        }
    }

    private fun openInCall() {
        startActivity(Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP))
        finish()
    }

    companion object {
        const val EXTRA_CALL = "call"
        const val EXTRA_ANSWER = "answer"

        /** Call whose screen is currently visible (null if none). */
        @Volatile var shownCall: String? = null
    }
}

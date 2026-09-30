package io.github.revocx35.webipphone

import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.lifecycle.lifecycleScope
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.testTagsAsResourceId
import io.github.revocx35.webipphone.ui.AppRoot
import io.github.revocx35.webipphone.ui.WebPhoneTheme
import kotlinx.coroutines.launch

class MainActivity : ComponentActivity() {
    override fun onNewIntent(intent: android.content.Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        applyDebugExtras(intent)
    }

    /** Debug builds: the emulator e2e replaces the microphone with a test tone. */
    private fun applyDebugExtras(intent: android.content.Intent) {
        if (BuildConfig.DEBUG && intent.hasExtra("test_tone")) {
            (application as WebPhoneApp).phone.audio.testTone = intent.getBooleanExtra("test_tone", false)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        val app = application as WebPhoneApp
        applyDebugExtras(intent)
        // During a call the in-call screen may show over the lock screen.
        lifecycleScope.launch {
            app.phone.state.collect { st ->
                val inCall = st.myCall != null
                if (Build.VERSION.SDK_INT >= 27) setShowWhenLocked(inCall)
            }
        }
        setContent {
            WebPhoneTheme {
                // testTag -> resource-id, so UI Automator (scripts/e2e.py) can find elements.
                Box(Modifier.fillMaxSize().semantics { testTagsAsResourceId = true }) { AppRoot(app) }
            }
        }
    }
}

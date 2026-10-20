package io.github.revocx35.webipphone.data

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import androidx.core.content.edit
import androidx.core.net.toUri
import io.github.revocx35.webipphone.MainActivity
import io.github.revocx35.webipphone.net.Authentik
import io.github.revocx35.webipphone.net.AuthentikTokenStore
import io.github.revocx35.webipphone.net.AuthentikTokens
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.OkHttpClient

/** The authentik sign-in, stored encrypted with [KeystoreCipher] (own key) like the session token. Never throws. */
class KeystoreAuthentikStore(context: Context, private val cipher: SecretCipher = KeystoreCipher("webphone_authentik")) : AuthentikTokenStore {
    private val sp = context.getSharedPreferences("authentik", Context.MODE_PRIVATE)

    override fun load(): AuthentikTokens? = runCatching {
        sp.getString("tokens", null)?.let { cipher.decrypt(it) }?.let { AuthentikTokens.fromJson(it) }
    }.getOrNull()

    override fun save(tokens: AuthentikTokens?) {
        runCatching { sp.edit(commit = true) { if (tokens == null) remove("tokens") else putString("tokens", cipher.encrypt(tokens.toJson())) } }
            .onFailure { runCatching { sp.edit(commit = true) { remove("tokens") } } }
    }
}

/**
 * The browser half of the authentik sign-in: opens authentik's login (a Custom Tab when the browser supports
 * it), waits for authentik to send the browser back to `<applicationId>:/authentik`, which
 * [AuthentikRedirectActivity] hands to [deliver].
 */
object AuthentikSignIn {
    /** The public OAuth client the owner creates in authentik for this app (docs/authentik.md). */
    const val DEFAULT_CLIENT_ID = "webiphone-android"

    private var waiting: CompletableDeferred<Uri>? = null

    fun redirectUri(context: Context): String = "${context.packageName}:/authentik"

    /**
     * Runs the whole sign-in for [site]; [location] is where the server redirected to (from the
     * `AuthentikRequiredException`). Throws an IOException with a user-facing message on failure.
     */
    suspend fun signIn(context: Context, authentik: Authentik, client: OkHttpClient, site: HttpUrl, location: HttpUrl?, clientId: String) {
        val authBase = withContext(Dispatchers.IO) { Authentik.discover(client, site, location) }
        val pending = authentik.begin(site, authBase, clientId.trim().ifEmpty { DEFAULT_CLIENT_ID }, redirectUri(context))
        val result = CompletableDeferred<Uri>()
        synchronized(this) {
            waiting?.cancel()
            waiting = result
        }
        open(context, pending.authorizeUrl.toString())
        val uri = try {
            result.await()
        } finally {
            synchronized(this) { if (waiting === result) waiting = null }
        }
        // Only the query (code, state or error) matters; give it an http(s) shape for OkHttp's parser.
        val redirect = "https://callback.invalid/?${uri.encodedQuery ?: ""}".toHttpUrl()
        withContext(Dispatchers.IO) { authentik.complete(pending, redirect) }
    }

    /** Stops waiting (the owner closed the browser without finishing). */
    fun cancel() {
        synchronized(this) { waiting?.cancel(); waiting = null }
    }

    fun deliver(uri: Uri): Boolean = synchronized(this) {
        val w = waiting ?: return false
        w.complete(uri)
        true
    }

    private fun open(context: Context, url: String) {
        val intent = Intent(Intent.ACTION_VIEW, url.toUri()).addCategory(Intent.CATEGORY_BROWSABLE)
        // The Custom Tabs request is only this extra (androidx.browser's CustomTabsIntent does the same).
        intent.putExtras(Bundle().apply { putBinder("android.support.customtabs.extra.SESSION", null) })
        if (context !is Activity) intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        context.startActivity(intent)
    }
}

/** Receives `<applicationId>:/authentik?code=…&state=…` from the browser and returns to the app. */
class AuthentikRedirectActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val uri = intent?.data
        if (uri != null && uri.scheme == packageName && uri.path == "/authentik") AuthentikSignIn.deliver(uri)
        // MainActivity is singleTask: this also closes the Custom Tab above it.
        startActivity(Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_SINGLE_TOP))
        finish()
    }
}

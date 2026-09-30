package io.github.revocx35.webipphone.net

import java.io.IOException
import java.security.KeyStore
import java.security.MessageDigest
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import java.util.Date
import javax.net.ssl.HostnameVerifier
import javax.net.ssl.SSLSession
import javax.net.ssl.TrustManagerFactory
import javax.net.ssl.X509TrustManager
import javax.net.ssl.HttpsURLConnection
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/** Thrown (as the cause of the TLS failure) when the server certificate is neither trusted nor pinned. */
class UntrustedCertificateException(
    val fingerprint: String,
    val subject: String,
    val issuer: String,
    val notAfter: Date,
    /** A different certificate was pinned before: possible interception. */
    val pinMismatch: Boolean,
) : CertificateException("The server's certificate is not trusted")

fun sha256Fingerprint(der: ByteArray): String =
    MessageDigest.getInstance("SHA-256").digest(der).joinToString(":") { "%02X".format(it) }

/** Finds an [UntrustedCertificateException] in a failure's cause chain. */
fun Throwable.untrustedCertificate(): UntrustedCertificateException? {
    var t: Throwable? = this
    val seen = HashSet<Throwable>()
    while (t != null && seen.add(t)) {
        if (t is UntrustedCertificateException) return t
        t = t.cause
    }
    return null
}

/**
 * Trust on first use: certificates valid for the system (or user-installed) CAs are accepted as
 * usual. Anything else is accepted only if its SHA-256 fingerprint equals the one the user
 * confirmed earlier (the admin page shows the server's fingerprint for comparison).
 */
class PinningTrustManager(private val pin: String?) : X509TrustManager {
    private val system: X509TrustManager = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm())
        .apply { init(null as KeyStore?) }
        .trustManagers.filterIsInstance<X509TrustManager>().first()

    override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) {
        if (chain.isEmpty()) throw CertificateException("empty certificate chain")
        val leaf = chain[0]
        val fp = sha256Fingerprint(leaf.encoded)
        if (pin != null && pin.equals(fp, ignoreCase = true)) {
            leaf.checkValidity()
            return
        }
        try {
            system.checkServerTrusted(chain, authType)
        } catch (e: CertificateException) {
            throw UntrustedCertificateException(fp, leaf.subjectX500Principal.name, leaf.issuerX500Principal.name, leaf.notAfter, pin != null)
                .apply { initCause(e) }
        }
    }

    override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) = throw CertificateException("no client auth")

    override fun getAcceptedIssuers(): Array<X509Certificate> = system.acceptedIssuers
}

/** A pinned certificate is bound to its fingerprint, not to a host name (self-signed certs
 *  rarely name the address the phone uses); other certificates need a matching name. */
class PinningHostnameVerifier(private val pin: String?) : HostnameVerifier {
    override fun verify(hostname: String, session: SSLSession): Boolean {
        val leaf = session.peerCertificates.firstOrNull() as? X509Certificate ?: return false
        if (pin != null && pin.equals(sha256Fingerprint(leaf.encoded), ignoreCase = true)) return true
        return HttpsURLConnection.getDefaultHostnameVerifier().verify(hostname, session)
    }
}

class ServerUrlException(message: String) : IOException(message)

/** Normalizes user input to https://host[:port] (no path). */
fun normalizeServerUrl(input: String): String {
    var s = input.trim()
    if (s.startsWith("http:", ignoreCase = true)) throw ServerUrlException("Use https:// - the app never sends passwords unencrypted")
    s = s.replace(Regex("^https:/*", RegexOption.IGNORE_CASE), "").trimEnd('/')
    if (s.isBlank()) throw ServerUrlException("Enter the server address")
    s = "https://$s"
    val url = s.toHttpUrlOrNull() ?: throw ServerUrlException("Not a valid address")
    if (url.encodedPath != "/" && url.encodedPath.isNotEmpty()) throw ServerUrlException("Enter only the address, without a path")
    val port = if (url.port == 443) "" else ":${url.port}"
    val host = if (url.host.contains(':')) "[${url.host}]" else url.host
    return "https://$host$port"
}

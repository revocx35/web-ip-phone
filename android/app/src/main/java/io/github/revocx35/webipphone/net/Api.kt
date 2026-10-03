package io.github.revocx35.webipphone.net

import java.io.IOException
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLContext
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.decodeFromJsonElement
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.WebSocket
import okhttp3.WebSocketListener

class ApiException(val status: Int, val code: String, message: String) : IOException(message)

/** REST + WebSocket client for one server, authenticated with a bearer token. */
class Api(val baseUrl: String, pin: String?, @Volatile var token: String? = null) {
    val http: OkHttpClient = run {
        val tm = PinningTrustManager(pin)
        val ctx = SSLContext.getInstance("TLS").apply { init(null, arrayOf(tm), null) }
        OkHttpClient.Builder()
            .sslSocketFactory(ctx.socketFactory, tm)
            .hostnameVerifier(PinningHostnameVerifier(pin))
            .connectTimeout(10, TimeUnit.SECONDS)
            .readTimeout(20, TimeUnit.SECONDS)
            .writeTimeout(20, TimeUnit.SECONDS)
            .pingInterval(25, TimeUnit.SECONDS)
            .retryOnConnectionFailure(true)
            .build()
    }

    private val jsonType = "application/json".toMediaType()

    suspend fun call(method: String, path: String, body: JsonElement? = null): JsonElement = withContext(Dispatchers.IO) {
        val b = Request.Builder().url("$baseUrl/api/v1$path")
            // Required by the server's CSRF check on requests without a bearer token (login).
            .header("X-Requested-With", "webphone")
        token?.let { b.header("Authorization", "Bearer $it") }
        val rb = when {
            body != null -> json.encodeToString(JsonElement.serializer(), body).toRequestBody(jsonType)
            method == "GET" || method == "DELETE" -> null
            else -> "{}".toRequestBody(jsonType)
        }
        b.method(method, rb)
        http.newCall(b.build()).execute().use { res ->
            val text = res.body.string()
            if (!res.isSuccessful) {
                val err = runCatching { json.decodeFromString(ApiErrorBody.serializer(), text) }.getOrNull()
                throw ApiException(res.code, err?.code ?: "http_${res.code}", err?.error?.ifBlank { null } ?: "Server error ${res.code}")
            }
            if (text.isBlank()) JsonObject(emptyMap()) else json.parseToJsonElement(text)
        }
    }

    suspend inline fun <reified T> get(path: String): T = json.decodeFromJsonElement(call("GET", path))
    suspend inline fun <reified T> post(path: String, body: Map<String, Any?> = emptyMap()): T = json.decodeFromJsonElement(call("POST", path, obj(body)))
    suspend inline fun <reified T> put(path: String, body: Map<String, Any?>): T = json.decodeFromJsonElement(call("PUT", path, obj(body)))
    suspend fun delete(path: String) { call("DELETE", path) }

    suspend fun info(): ServerInfo = get("/info")

    suspend fun login(username: String, password: String, device: String): LoginResponse =
        post("/auth/login", mapOf("username" to username, "password" to password, "client" to "app", "deviceName" to device))

    suspend fun mfa(ticket: String, code: String): LoginResponse = post("/auth/mfa", mapOf("ticket" to ticket, "code" to code))

    suspend fun logout() { runCatching { call("POST", "/auth/logout", obj(emptyMap())) } }

    suspend fun me(): Me = get("/me")

    suspend fun changePassword(current: String, new: String): Me = post("/me/password", mapOf("current" to current, "new" to new))

    suspend fun totpSetup(): TotpSetup = post("/me/totp/setup")

    suspend fun totpEnable(code: String): RecoveryCodes = post("/me/totp/enable", mapOf("code" to code))

    suspend fun phones(): PhonesResponse = get("/phones")

    suspend fun addPhone(pbxId: Long, sipUser: String, authUser: String, password: String, displayName: String, label: String, register: Boolean): OwnPhone =
        post("/phones", mapOf("pbxId" to pbxId, "sipUser" to sipUser, "authUser" to authUser, "password" to password,
            "displayName" to displayName, "label" to label, "register" to register, "verify" to true))

    suspend fun deletePhone(id: Long) = delete("/phones/$id")

    suspend fun calls(): List<CallRecord> = get("/calls?limit=200")

    suspend fun clearCalls() = delete("/calls")

    suspend fun contacts(): List<Contact> = get<ContactsResponse>("/contacts").contacts

    /** Creates ([id] null) or updates a contact. */
    suspend fun saveContact(id: Long?, name: String, numbers: List<ContactNumber>, favorite: Boolean, shared: Boolean): Contact {
        val body = mapOf("name" to name, "numbers" to numbers.map { mapOf("label" to it.label, "number" to it.number) },
            "favorite" to favorite, "shared" to shared)
        return if (id == null) post("/contacts", body) else put("/contacts/$id", body)
    }

    suspend fun deleteContact(id: Long) = delete("/contacts/$id")

    suspend fun setFavorite(id: Long, on: Boolean): Contact = put("/contacts/$id/favorite", mapOf("favorite" to on))

    fun webSocket(listener: WebSocketListener): WebSocket {
        val req = Request.Builder().url("$baseUrl/api/v1/ws").header("Authorization", "Bearer ${token ?: ""}").build()
        return http.newWebSocket(req, listener)
    }

    companion object {
        fun obj(m: Map<String, Any?>): JsonObject = toJson(m) as JsonObject

        fun toJson(v: Any?): JsonElement = when (v) {
            null -> JsonNull
            is JsonElement -> v
            is String -> JsonPrimitive(v)
            is Number -> JsonPrimitive(v)
            is Boolean -> JsonPrimitive(v)
            is Map<*, *> -> JsonObject(v.entries.associate { (k, x) -> k.toString() to toJson(x) })
            is List<*> -> JsonArray(v.map { toJson(it) })
            else -> JsonPrimitive(v.toString())
        }
    }
}

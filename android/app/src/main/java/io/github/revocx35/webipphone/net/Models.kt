package io.github.revocx35.webipphone.net

import kotlinx.serialization.Serializable
import kotlinx.serialization.Transient
import kotlinx.serialization.json.Json

/** Mirrors the server's JSON (internal/httpapi, internal/phone/protocol.go). */
val json = Json {
    ignoreUnknownKeys = true
    explicitNulls = false
    encodeDefaults = true
}

@Serializable
data class ServerInfo(val name: String = "", val version: String = "", val setupRequired: Boolean = false, val api: Int = 0)

@Serializable
data class Me(
    val id: Long,
    val username: String,
    val displayName: String = "",
    val isAdmin: Boolean = false,
    val totpEnabled: Boolean = false,
    val mustChangePassword: Boolean = false,
    val mustEnroll2fa: Boolean = false,
    val twoFaRequired: Boolean = false,
    val passwordMinLength: Int = 10,
    val sessionId: Long = 0,
)

@Serializable
data class LoginResponse(val user: Me? = null, val token: String? = null, val mfaRequired: Boolean = false, val ticket: String? = null)

@Serializable
data class RegState(val status: String = "off", val error: String? = null)

@Serializable
data class PhoneView(
    val id: Long,
    val label: String = "",
    val sipUser: String = "",
    val displayName: String = "",
    val pbxId: Long = 0,
    val pbxName: String = "",
    val owned: Boolean = false,
    val register: Boolean = true,
    val reg: RegState = RegState(),
    val online: Boolean = false,
) {
    val title: String get() = label.ifBlank { displayName.ifBlank { sipUser } }
}

@Serializable
data class CallView(
    val id: String,
    val phoneId: Long = 0,
    val direction: String = "out",
    val remote: String = "",
    val remoteName: String = "",
    val state: String = "",
    val codec: String = "",
    val hold: Boolean = false,
    val remoteHold: Boolean = false,
    val startedAt: String = "",
    val answeredAt: String? = null,
    val endReason: String? = null,
    val endStatus: String? = null,
    val attached: Boolean = false,
    val mine: Boolean = false,
    /** Set on the device from the phone book (PhoneClient), never sent by the server. */
    @Transient val contactName: String = "",
    @Transient val contactLabel: String = "",
) {
    /** Contact name, else the name the PBX sent, else the number. */
    val who: String get() = contactName.ifBlank { remoteName.ifBlank { remote } }
    /** Second line under [who]: the number (with the contact's label for it), or "" if [who] is the number. */
    val numberLine: String get() = if (who == remote) "" else listOf(contactLabel, remote).filter { it.isNotBlank() }.joinToString(" · ")
    val ended: Boolean get() = state == "ended"
}

@Serializable
data class CallRecord(
    val id: Long,
    val phoneId: Long = 0,
    val phoneLabel: String = "",
    val pbxName: String = "",
    val direction: String = "out",
    val remote: String = "",
    val remoteName: String = "",
    val startedAt: String = "",
    val answeredAt: String? = null,
    val endedAt: String? = null,
    val status: String = "",
    val reason: String = "",
)

@Serializable
data class ContactNumber(val label: String = "", val number: String = "")

@Serializable
data class Contact(
    val id: Long,
    val name: String = "",
    val numbers: List<ContactNumber> = emptyList(),
    val favorite: Boolean = false,
    /** In the phone book of all users (managed by administrators). */
    val shared: Boolean = false,
    val editable: Boolean = false,
)

@Serializable
data class ContactsResponse(val contacts: List<Contact> = emptyList())

@Serializable
data class OwnPhone(
    val id: Long,
    val pbxId: Long = 0,
    val ownerId: Long = 0,
    val label: String = "",
    val sipUser: String = "",
    val authUser: String = "",
    val displayName: String = "",
    val register: Boolean = true,
    val pbxName: String = "",
    val owned: Boolean = false,
    val usable: Boolean = true,
    val reg: RegState = RegState(),
)

@Serializable
data class PbxRef(val id: Long, val name: String, val mode: String = "", val canAddPhones: Boolean = false, val restricted: Boolean = false)

@Serializable
data class PhonesResponse(val phones: List<OwnPhone> = emptyList(), val pbxs: List<PbxRef> = emptyList())

@Serializable
data class TotpSetup(val secret: String, val uri: String, val qrPng: String = "")

@Serializable
data class RecoveryCodes(val recoveryCodes: List<String> = emptyList())

@Serializable
data class ApiErrorBody(val error: String = "", val code: String = "")

/** Server -> client WebSocket message (union of all fields). */
@Serializable
data class WsMessage(
    val type: String,
    val req: String? = null,
    val code: String? = null,
    val error: String? = null,
    val call: CallView? = null,
    val calls: List<CallView>? = null,
    val phones: List<PhoneView>? = null,
    val version: String? = null,
    val digit: String? = null,
)

/** Handles "call" whose value is a string in acks and an object in call events. */
@Serializable
data class WsAck(val type: String, val req: String? = null, val call: String? = null)

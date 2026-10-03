package io.github.revocx35.webipphone.ui

import android.app.Activity
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.provider.ContactsContract.CommonDataKinds.Phone
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContract
import androidx.compose.foundation.clickable
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.testTagsAsResourceId
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.revocx35.webipphone.Session
import io.github.revocx35.webipphone.WebPhoneApp
import io.github.revocx35.webipphone.net.Contact
import io.github.revocx35.webipphone.net.ContactNumber
import kotlinx.coroutines.launch

private val FavoriteColor = Color(0xFFD97706)

/** A contact being created or edited (id null = new). */
data class ContactDraft(val id: Long?, val name: String, val numbers: List<ContactNumber>, val favorite: Boolean = false, val shared: Boolean = false) {
    companion object {
        fun of(c: Contact) = ContactDraft(c.id, c.name, c.numbers, c.favorite, c.shared)
        fun new(name: String = "", number: String = "") = ContactDraft(null, name, listOf(ContactNumber("", number)))
    }
}

private fun ContactNumber.line() = if (label.isBlank()) number else "$label · $number"

/** Phone book: search, favorites first, tap a contact for its numbers, call with one tap. */
@Composable
fun ContactsScreen(app: WebPhoneApp) {
    val book by app.phone.contacts.collectAsState()
    val scope = rememberCoroutineScope()
    var query by rememberSaveable { mutableStateOf("") }
    var openId by remember { mutableStateOf<Long?>(null) }
    var edit by remember { mutableStateOf<ContactDraft?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var pending by remember { mutableStateOf<String?>(null) }
    fun closeDialogs() {
        openId = null
        edit = null
    }
    val askMic = rememberMicPermission {
        val n = pending ?: return@rememberMicPermission
        pending = null
        scope.launch { runCatching { app.phone.dial(n) }.onFailure { error = it.message } }
    }
    val call: (String) -> Unit = { n -> error = null; pending = n; askMic() }
    val list = if (query.isBlank()) book.list.filter { it.favorite } + book.list.filter { !it.favorite } else book.search(query)

    Box(Modifier.fillMaxSize()) {
        // Focusable itself: when a dialog opens or closes, Compose moves focus to the first focusable
        // (otherwise the search field, which would pop up the keyboard).
        Column(Modifier.fillMaxSize().focusable()) {
            Text("Contacts", style = MaterialTheme.typography.titleLarge, modifier = Modifier.padding(start = 20.dp, top = 20.dp, end = 20.dp, bottom = 8.dp))
            OutlinedTextField(query, { query = it }, placeholder = { Text("Search name or number") }, singleLine = true,
                leadingIcon = { Icon(AppIcons.Search, null) },
                trailingIcon = { if (query.isNotEmpty()) IconButton({ query = "" }) { Icon(AppIcons.Close, "Clear search") } },
                modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp).testTag("contact-search"))
            ErrorText(error)
            if (list.isEmpty()) {
                Text(if (query.isBlank()) "No contacts yet. Add the people you call often; they are synced with the web app."
                    else "No matching contacts", Modifier.fillMaxWidth().padding(40.dp), textAlign = TextAlign.Center,
                    color = MaterialTheme.colorScheme.onSurfaceVariant)
            } else {
                LazyColumn(contentPadding = PaddingValues(top = 8.dp, bottom = 88.dp)) {
                    items(list, key = { it.id }) { c ->
                        ContactRow(c, onOpen = { openId = c.id }, onCall = call)
                        HorizontalDivider()
                    }
                }
            }
        }
        FloatingActionButton({ edit = ContactDraft.new() }, Modifier.align(Alignment.BottomEnd).padding(16.dp).testTag("add-contact")) {
            Icon(AppIcons.Plus, "Add contact")
        }
    }

    book.list.firstOrNull { it.id == openId }?.let { c ->
        ContactDetailDialog(app, c, onCall = { closeDialogs(); call(it) }, onEdit = { openId = null; edit = ContactDraft.of(c) }, onClose = ::closeDialogs)
    }
    edit?.let { d -> ContactEditDialog(app, d, onDone = ::closeDialogs) }
}

@Composable
private fun ContactRow(c: Contact, onOpen: () -> Unit, onCall: (String) -> Unit) {
    var menu by remember { mutableStateOf(false) }
    Row(Modifier.fillMaxWidth().clickable(onClick = onOpen).padding(start = 16.dp, end = 8.dp, top = 10.dp, bottom = 10.dp),
        verticalAlignment = Alignment.CenterVertically) {
        Avatar(c.name, 42.dp)
        Spacer(Modifier.width(14.dp))
        Column(Modifier.weight(1f)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(c.name, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false))
                if (c.favorite) Icon(AppIcons.StarFilled, "Favorite", tint = FavoriteColor, modifier = Modifier.padding(start = 6.dp).size(16.dp))
                if (c.shared) Text("Shared", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.primary,
                    modifier = Modifier.padding(start = 6.dp))
            }
            val first = c.numbers.firstOrNull()?.line() ?: ""
            Text(if (c.numbers.size > 1) "$first  +${c.numbers.size - 1}" else first, style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        Box {
            IconButton({ if (c.numbers.size == 1) onCall(c.numbers[0].number) else menu = true }) {
                Icon(AppIcons.Phone, "Call ${c.name}", tint = MaterialTheme.colorScheme.primary)
            }
            DropdownMenu(menu, { menu = false }) {
                c.numbers.forEach { n -> DropdownMenuItem(text = { Text(n.line()) }, onClick = { menu = false; onCall(n.number) }) }
            }
        }
    }
}

@Composable
private fun ContactDetailDialog(app: WebPhoneApp, c: Contact, onCall: (String) -> Unit, onEdit: () -> Unit, onClose: () -> Unit) {
    val scope = rememberCoroutineScope()
    var error by remember { mutableStateOf<String?>(null) }
    AlertDialog(
        onDismissRequest = onClose,
        title = {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(c.name, Modifier.weight(1f), maxLines = 2, overflow = TextOverflow.Ellipsis)
                IconButton({
                    scope.launch {
                        runCatching { app.api?.setFavorite(c.id, !c.favorite) }.onSuccess { app.phone.reloadContacts() }.onFailure { error = it.message }
                    }
                }) {
                    if (c.favorite) Icon(AppIcons.StarFilled, "Remove from favorites", tint = FavoriteColor)
                    else Icon(AppIcons.Star, "Add to favorites")
                }
            }
        },
        text = {
            Column(Modifier.semantics { testTagsAsResourceId = true }.verticalScroll(rememberScrollState())) {
                c.numbers.forEach { n ->
                    Row(Modifier.fillMaxWidth().clickable { onCall(n.number) }.padding(vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            if (n.label.isNotBlank()) Text(n.label, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            Text(n.number, style = MaterialTheme.typography.bodyLarge)
                        }
                        Icon(AppIcons.Phone, "Call ${n.number}", tint = MaterialTheme.colorScheme.primary)
                    }
                }
                if (c.shared) {
                    Text("Shared with all users" + if (c.editable) "" else " · managed by your administrator",
                        style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 10.dp))
                }
                ErrorText(error)
            }
        },
        confirmButton = { if (c.editable) TextButton(onEdit) { Text("Edit") } },
        dismissButton = { TextButton(onClose) { Text("Close") } },
    )
}

/** Picks one phone number from the device's contacts. The system grants read access to the picked
 *  entry only, so the app needs no contacts permission. */
private class PickPhoneNumber : ActivityResultContract<Unit, Uri?>() {
    override fun createIntent(context: Context, input: Unit) = Intent(Intent.ACTION_PICK).setType(Phone.CONTENT_TYPE)
    override fun parseResult(resultCode: Int, intent: Intent?): Uri? = intent?.data?.takeIf { resultCode == Activity.RESULT_OK }
}

private fun readPicked(ctx: Context, uri: Uri): Pair<String, ContactNumber>? = runCatching {
    ctx.contentResolver.query(uri, arrayOf(Phone.DISPLAY_NAME, Phone.NUMBER, Phone.TYPE, Phone.LABEL), null, null, null)?.use { c ->
        if (!c.moveToFirst()) return@use null
        val label = Phone.getTypeLabel(ctx.resources, c.getInt(2), c.getString(3) ?: "").toString()
        (c.getString(0) ?: "") to ContactNumber(label.take(32), c.getString(1) ?: "")
    }
}.getOrNull()

/** Creates or edits a contact (also used by Recents to save a caller). */
@Composable
fun ContactEditDialog(app: WebPhoneApp, draft: ContactDraft, onDone: () -> Unit) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val session by app.session.collectAsState()
    val isAdmin = (session as? Session.Active)?.me?.isAdmin == true
    var name by remember { mutableStateOf(draft.name) }
    val numbers = remember { mutableStateListOf(*draft.numbers.ifEmpty { listOf(ContactNumber()) }.toTypedArray()) }
    var favorite by remember { mutableStateOf(draft.favorite) }
    var shared by remember { mutableStateOf(draft.shared) }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var confirmDelete by remember { mutableStateOf(false) }
    val picker = rememberLauncherForActivityResult(PickPhoneNumber()) { uri ->
        val (n, num) = uri?.let { readPicked(ctx, it) } ?: return@rememberLauncherForActivityResult
        if (name.isBlank()) name = n
        val blank = numbers.indexOfFirst { it.number.isBlank() }
        if (blank >= 0) numbers[blank] = num else if (numbers.size < 10) numbers.add(num)
    }
    fun submit(block: suspend () -> Unit) {
        busy = true
        error = null
        scope.launch {
            try {
                block()
                app.phone.reloadContacts()
                onDone()
            } catch (e: Exception) {
                error = e.message
            } finally {
                busy = false
            }
        }
    }

    AlertDialog(
        onDismissRequest = onDone,
        title = { Text(if (draft.id == null) "New contact" else "Edit contact") },
        text = {
            // Dialogs have their own composition: expose test tags here too (UI Automator e2e).
            Column(Modifier.semantics { testTagsAsResourceId = true }.verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedButton({
                    try { picker.launch(Unit) } catch (_: ActivityNotFoundException) { error = "No contacts app on this device" }
                }, Modifier.fillMaxWidth()) { Text("From phone contacts") }
                OutlinedTextField(name, { name = it.take(80) }, label = { Text("Name") }, singleLine = true,
                    modifier = Modifier.fillMaxWidth().testTag("contact-name"))
                numbers.forEachIndexed { i, n ->
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        OutlinedTextField(n.label, { numbers[i] = n.copy(label = it.take(32)) }, label = { Text("Label") }, singleLine = true,
                            modifier = Modifier.weight(0.38f).testTag("contact-label-$i"))
                        Spacer(Modifier.width(6.dp))
                        OutlinedTextField(n.number, { numbers[i] = n.copy(number = it.take(80)) }, label = { Text("Number") }, singleLine = true,
                            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Phone),
                            modifier = Modifier.weight(0.62f).testTag("contact-number-$i"))
                        if (numbers.size > 1) IconButton({ numbers.removeAt(i) }) { Icon(AppIcons.Close, "Remove number") }
                    }
                }
                if (numbers.size < 10) TextButton({ numbers.add(ContactNumber()) }) { Text("Add number") }
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text("Favorite", Modifier.weight(1f))
                    Switch(favorite, { favorite = it })
                }
                if (isAdmin) Row(verticalAlignment = Alignment.CenterVertically) {
                    Column(Modifier.weight(1f)) {
                        Text("Shared with all users")
                        Text("Only administrators can change shared contacts.", style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    Switch(shared, { shared = it })
                }
                if (draft.id != null) TextButton({ confirmDelete = true }, enabled = !busy) {
                    Text("Delete contact", color = MaterialTheme.colorScheme.error)
                }
                ErrorText(error)
            }
        },
        confirmButton = {
            TextButton({
                val list = numbers.filter { it.number.isNotBlank() }.map { ContactNumber(it.label.trim(), it.number.trim()) }
                when {
                    name.isBlank() -> error = "Enter a name"
                    list.isEmpty() -> error = "Enter at least one number"
                    else -> submit { app.api?.saveContact(draft.id, name.trim(), list, favorite, shared) ?: throw IllegalStateException("Not signed in") }
                }
            }, enabled = !busy) { Text(if (busy) "Saving…" else "Save") }
        },
        dismissButton = { TextButton(onDone) { Text("Cancel") } },
    )
    if (confirmDelete) {
        AlertDialog(
            onDismissRequest = { confirmDelete = false },
            title = { Text("Delete ${name.ifBlank { "contact" }}?") },
            text = { if (draft.shared) Text("It is removed for all users.") },
            confirmButton = { TextButton({ confirmDelete = false; submit { draft.id?.let { app.api?.deleteContact(it) } } }) { Text("Delete") } },
            dismissButton = { TextButton({ confirmDelete = false }) { Text("Cancel") } },
        )
    }
}

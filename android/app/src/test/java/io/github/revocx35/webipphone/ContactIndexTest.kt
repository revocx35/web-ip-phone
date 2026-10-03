package io.github.revocx35.webipphone

import io.github.revocx35.webipphone.net.Api
import io.github.revocx35.webipphone.net.CallView
import io.github.revocx35.webipphone.net.Contact
import io.github.revocx35.webipphone.net.ContactNumber
import io.github.revocx35.webipphone.phone.ContactIndex
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** Same cases as web/test/contactIndex.test.ts: both clients must name callers the same way. */
class ContactIndexTest {
    private fun contact(id: Long, name: String, vararg numbers: String, favorite: Boolean = false) =
        Contact(id, name, numbers.map { ContactNumber("", it) }, favorite = favorite)

    private val book = ContactIndex(listOf(
        contact(1, "Alice", "+49 151 1234567", "1005"),
        contact(2, "Bob", "030 9876543"),
        contact(3, "José Núñez", "john.doe-2"),
        contact(4, "Reception", "2001", favorite = true),
        contact(5, "Reception copy", "2001"),
    ))

    @Test
    fun canonicalNumbers() {
        assertEquals("+491511234567", ContactIndex.canon("+49 (151) 123-45.67"))
        assertEquals("john.doe-2", ContactIndex.canon("John.Doe-2"))
    }

    @Test
    fun lookupExactDigitsAndLastSevenDigits() {
        assertEquals("Alice", book.lookup("1005")?.contact?.name)
        assertEquals("Alice", book.lookup("+491511234567")?.contact?.name)
        assertEquals("Alice", book.lookup("00491511234567")?.contact?.name)
        assertEquals("Alice", book.lookup("01511234567")?.contact?.name)
        assertEquals("Bob", book.lookup("+49309876543")?.contact?.name)
        assertEquals("José Núñez", book.lookup("JOHN.DOE-2")?.contact?.name)
        assertNull(book.lookup("1006"))
        assertNull(book.lookup("005")) // short numbers only match exactly
        assertNull(book.lookup(""))
        assertNull(book.lookup("anonymous"))
    }

    @Test
    fun favoritesWinWhenNumbersCollide() {
        assertEquals("Reception", book.lookup("2001")?.contact?.name)
    }

    @Test
    fun searchByNameWithoutAccentsAndByNumber() {
        assertEquals(listOf(3L), book.search("jose").map { it.id })
        assertEquals(listOf(2L), book.search("98765").map { it.id })
        assertEquals(listOf(1L), book.search("151 123").map { it.id })
        assertEquals(5, book.search("").size)
    }

    @Test
    fun keypadSuggestions() {
        assertNull(book.suggest("1"))
        assertEquals("1005", book.suggest("100")?.number?.number)
        assertEquals("Reception", book.suggest("2001")?.contact?.name)
        assertEquals("Bob", book.suggest("98")?.contact?.name)
        assertNull(book.suggest("777"))
    }

    @Test
    fun callNamesPreferContacts() {
        val call = CallView("c1", remote = "2001", remoteName = "PBX name")
        assertEquals("PBX name", call.who)
        assertEquals("2001", call.numberLine)
        val named = call.copy(contactName = "Reception", contactLabel = "Desk")
        assertEquals("Reception", named.who)
        assertEquals("Desk · 2001", named.numberLine)
        assertEquals("", CallView("c2", remote = "2001").numberLine)
    }

    @Test
    fun nestedJsonBodies() {
        val body = Api.obj(mapOf("name" to "A", "numbers" to listOf(mapOf("label" to "", "number" to "1")), "favorite" to true, "ids" to listOf(1L)))
        assertEquals("""{"name":"A","numbers":[{"label":"","number":"1"}],"favorite":true,"ids":[1]}""", body.toString())
    }
}

package io.github.revocx35.webipphone.phone

import io.github.revocx35.webipphone.net.Contact
import io.github.revocx35.webipphone.net.ContactNumber
import java.text.Normalizer

data class ContactHit(val contact: Contact, val number: ContactNumber)

/** Finds contacts by number and searches the phone book. Same rules as web/src/contactIndex.ts:
 *  exact match, then same digits, then the same last 7 digits (+49 30 1234567 = 0301234567). */
class ContactIndex(val list: List<Contact>) {
    private val exact = HashMap<String, ContactHit>()
    private val digits = HashMap<String, ContactHit>()
    private val tail = HashMap<String, ContactHit>()

    init {
        // Favorites win when two contacts share a number.
        for (contact in list.filter { it.favorite } + list.filter { !it.favorite }) {
            for (number in contact.numbers) {
                val hit = ContactHit(contact, number)
                val k = canon(number.number)
                exact.putIfAbsent(k, hit)
                if (!PHONE_LIKE.matches(k)) continue
                val d = k.removePrefix("+")
                digits.putIfAbsent(d, hit)
                if (d.length >= TAIL) tail.putIfAbsent(d.takeLast(TAIL), hit)
            }
        }
    }

    fun lookup(remote: String?): ContactHit? {
        if (remote.isNullOrBlank()) return null
        val k = canon(remote)
        exact[k]?.let { return it }
        if (!PHONE_LIKE.matches(k)) return null
        val d = k.removePrefix("+")
        return digits[d] ?: if (d.length >= TAIL) tail[d.takeLast(TAIL)] else null
    }

    /** Contacts whose name or numbers contain the query. */
    fun search(query: String): List<Contact> {
        val q = fold(query.trim())
        if (q.isEmpty()) return list
        val qn = canon(query.trim())
        return list.filter { c -> fold(c.name).contains(q) || (qn.isNotEmpty() && c.numbers.any { canon(it.number).contains(qn) }) }
    }

    /** The contact number that best completes what was typed on the keypad. */
    fun suggest(typed: String): ContactHit? {
        val t = canon(typed)
        if (t.length < 2) return null
        lookup(typed)?.let { return it }
        var best: ContactHit? = null
        var bestRank = 9
        for (contact in list) for (number in contact.numbers) {
            val pos = canon(number.number).indexOf(t)
            if (pos < 0) continue
            val rank = (if (pos == 0) 0 else 2) + (if (contact.favorite) 0 else 1)
            if (rank < bestRank) { best = ContactHit(contact, number); bestRank = rank }
        }
        return best
    }

    companion object {
        val EMPTY = ContactIndex(emptyList())
        private const val TAIL = 7
        private val PHONE_LIKE = Regex("^\\+?\\d+$")
        private val LETTER = Regex("\\p{L}")
        private val SEPARATORS = Regex("[\\s()/]")
        private val SEPARATORS_DIGITS = Regex("[\\s()/.-]")
        private val MARKS = Regex("\\p{M}")

        /** Comparable form of a number: lower case, without separators; SIP names with letters keep . and -. */
        fun canon(n: String): String = n.lowercase().replace(if (LETTER.containsMatchIn(n)) SEPARATORS else SEPARATORS_DIGITS, "")

        /** Lower case without diacritics ("jose" finds "José"). */
        fun fold(s: String): String = Normalizer.normalize(s, Normalizer.Form.NFD).replace(MARKS, "").lowercase()
    }
}

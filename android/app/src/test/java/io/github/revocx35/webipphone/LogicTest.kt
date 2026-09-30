package io.github.revocx35.webipphone

import io.github.revocx35.webipphone.net.ServerUrlException
import io.github.revocx35.webipphone.net.normalizeServerUrl
import io.github.revocx35.webipphone.net.sha256Fingerprint
import io.github.revocx35.webipphone.phone.Decimator
import io.github.revocx35.webipphone.phone.G711
import io.github.revocx35.webipphone.phone.JitterBuffer
import kotlin.math.PI
import kotlin.math.abs
import kotlin.math.sin
import kotlin.math.sqrt
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class LogicTest {
    @Test
    fun g711RoundTripMatchesTheServer() {
        val out = ShortArray(1)
        for (alaw in listOf(false, true)) {
            var v = -32768
            while (v <= 32767) {
                val enc = if (alaw) G711.encodeAlaw(v) else G711.encodeUlaw(v)
                G711.decode(byteArrayOf(enc), 0, 1, alaw, out)
                assertTrue("alaw=$alaw $v -> ${out[0]}", abs(out[0] - v) <= 16 + abs(v) / 12)
                v += 101
            }
        }
        // Same bytes as internal/media/g711.go for a few reference values.
        assertEquals(0xFF.toByte(), G711.encodeUlaw(0))
        assertEquals(0xD5.toByte(), G711.encodeAlaw(0))
        assertEquals(0x80.toByte(), G711.encodeUlaw(32767))
        assertEquals(0x00.toByte(), G711.encodeUlaw(-32768))
    }

    @Test
    fun jitterBufferBuffersAndTrimsBursts() {
        val jb = JitterBuffer(target = 3)
        val f = ByteArray(160)
        jb.push(f); jb.push(f)
        assertNull("waits for the target depth", jb.pop())
        jb.push(f)
        assertNotNull(jb.pop())
        repeat(20) { jb.push(f) }
        assertTrue("burst trimmed", jb.size() <= 3 + 5)
        while (jb.pop() != null) Unit
        assertEquals(1, jb.underruns)
        assertEquals("target grows after an underrun", 4, jb.currentTarget())
    }

    @Test
    fun decimatorKeepsVoiceAndRemovesAliases() {
        fun rmsAfter(freq: Double): Double {
            val d = Decimator(6)
            val input = ShortArray(4800) { (8000 * sin(2 * PI * freq * it / 48000)).toInt().toShort() }
            val out = ShortArray(800)
            val n = d.process(input, input.size, out)
            assertEquals(800, n)
            var s = 0.0
            for (i in 200 until n) s += out[i].toDouble() * out[i]
            return sqrt(s / (n - 200))
        }
        assertTrue("1 kHz passes", rmsAfter(1000.0) > 5000)
        assertTrue("6 kHz (would alias to 2 kHz) is removed", rmsAfter(6000.0) < 150)
    }

    @Test
    fun serverUrls() {
        assertEquals("https://phone.example.com", normalizeServerUrl(" phone.example.com/ "))
        assertEquals("https://10.0.2.2:8443", normalizeServerUrl("https://10.0.2.2:8443"))
        assertEquals("https://example.com", normalizeServerUrl("https://example.com:443"))
        for (bad in listOf("", "http://example.com", "https://example.com/admin", "https://")) {
            try {
                normalizeServerUrl(bad)
                throw AssertionError("accepted $bad")
            } catch (_: ServerUrlException) {
            }
        }
    }

    @Test
    fun fingerprintFormat() {
        val fp = sha256Fingerprint("abc".toByteArray())
        assertEquals("BA:78:16:BF", fp.take(11))
        assertEquals(95, fp.length)
    }
}

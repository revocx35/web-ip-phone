package io.github.revocx35.webipphone.phone

/** G.711 µ-law / A-law, identical to the server (internal/media/g711.go). */
object G711 {
    private val ulawDecode = ShortArray(256) { decodeUlaw(it) }
    private val alawDecode = ShortArray(256) { decodeAlaw(it) }

    private fun decodeUlaw(b: Int): Short {
        val u = b.inv() and 0xff
        var t = ((u and 0x0f) shl 3) + 0x84
        t = t shl ((u and 0x70) shr 4)
        return (if (u and 0x80 != 0) 0x84 - t else t - 0x84).toShort()
    }

    private fun decodeAlaw(b: Int): Short {
        val a = b xor 0x55
        var t = (a and 0x0f) shl 4
        when (val seg = (a and 0x70) shr 4) {
            0 -> t += 8
            1 -> t += 0x108
            else -> { t += 0x108; t = t shl (seg - 1) }
        }
        return (if (a and 0x80 != 0) t else -t).toShort()
    }

    fun encodeUlaw(sample: Int): Byte {
        var s = sample
        var sign = 0
        if (s < 0) { s = -s; sign = 0x80 }
        if (s > 32635) s = 32635
        s += 0x84
        var exp = 7
        var mask = 0x4000
        while (s and mask == 0 && exp > 0) { exp--; mask = mask shr 1 }
        val mant = (s shr (exp + 3)) and 0x0f
        return (sign or (exp shl 4) or mant).inv().toByte()
    }

    fun encodeAlaw(sample: Int): Byte {
        var s = sample
        val mask: Int
        if (s >= 0) mask = 0xd5 else { mask = 0x55; s = -s - 1 }
        if (s > 32767) s = 32767
        val seg = when {
            s < 256 -> 0; s < 512 -> 1; s < 1024 -> 2; s < 2048 -> 3
            s < 4096 -> 4; s < 8192 -> 5; s < 16384 -> 6; else -> 7
        }
        var aval = if (seg < 2) (s shr 4) and 0x0f else (s shr (seg + 3)) and 0x0f
        aval = aval or (seg shl 4)
        return (aval xor mask).toByte()
    }

    fun encode(pcm: ShortArray, alaw: Boolean, out: ByteArray, outOffset: Int = 0) {
        for (i in pcm.indices) out[outOffset + i] = if (alaw) encodeAlaw(pcm[i].toInt()) else encodeUlaw(pcm[i].toInt())
    }

    fun decode(data: ByteArray, offset: Int, length: Int, alaw: Boolean, out: ShortArray) {
        val table = if (alaw) alawDecode else ulawDecode
        for (i in 0 until length) out[i] = table[data[offset + i].toInt() and 0xff]
    }

    fun silence(alaw: Boolean): Byte = if (alaw) 0xD5.toByte() else 0xFF.toByte()
}

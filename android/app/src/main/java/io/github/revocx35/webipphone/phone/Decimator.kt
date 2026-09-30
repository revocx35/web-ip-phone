package io.github.revocx35.webipphone.phone

import kotlin.math.PI
import kotlin.math.cos
import kotlin.math.sin

/**
 * Integer-factor downsampler to 8 kHz with a windowed-sinc low-pass (for devices that do not
 * record at 8 kHz directly).
 */
class Decimator(private val factor: Int, taps: Int = 16 * factor + 1) {
    private val h: DoubleArray
    private val hist: DoubleArray
    private var pos = 0
    private var phase = 0

    init {
        require(factor >= 1)
        val cutoff = 3600.0 / (8000.0 * factor) // normalized to the input rate
        val m = taps - 1
        h = DoubleArray(taps) { i ->
            val x = i - m / 2.0
            val sinc = if (x == 0.0) 2 * cutoff else sin(2 * PI * cutoff * x) / (PI * x)
            val w = 0.42 - 0.5 * cos(2 * PI * i / m) + 0.08 * cos(4 * PI * i / m) // Blackman
            sinc * w
        }
        val sum = h.sum()
        for (i in h.indices) h[i] /= sum
        hist = DoubleArray(taps)
    }

    /** Feeds input samples and returns the output samples produced. */
    fun process(input: ShortArray, n: Int, out: ShortArray): Int {
        if (factor == 1) {
            System.arraycopy(input, 0, out, 0, n)
            return n
        }
        var o = 0
        for (i in 0 until n) {
            hist[pos] = input[i].toDouble()
            pos = (pos + 1) % hist.size
            if (++phase == factor) {
                phase = 0
                var acc = 0.0
                var k = pos
                for (j in h.indices) {
                    acc += h[j] * hist[k]
                    k = (k + 1) % hist.size
                }
                out[o++] = acc.coerceIn(-32768.0, 32767.0).toInt().toShort()
            }
        }
        return o
    }
}

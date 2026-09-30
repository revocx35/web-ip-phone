package io.github.revocx35.webipphone.phone

import java.util.ArrayDeque

/**
 * Audio from the server arrives over TCP: never lost, but in bursts after a network stall.
 * Holds 20 ms frames; the player takes one per 20 ms. After a burst the backlog is dropped so
 * the delay stays low; after an underrun the target depth grows a little.
 */
class JitterBuffer(
    private var target: Int = 3,
    private val minTarget: Int = 2,
    private val maxTarget: Int = 10,
) {
    private val q = ArrayDeque<ByteArray>()
    private var buffering = true
    private var stableFrames = 0
    var underruns = 0
        private set

    @Synchronized
    fun push(frame: ByteArray) {
        q.addLast(frame)
        if (q.size > target + 5) {
            while (q.size > target) q.removeFirst()
        }
    }

    /** Next frame to play, or null for silence (buffering). */
    @Synchronized
    fun pop(): ByteArray? {
        if (buffering) {
            if (q.size < target) return null
            buffering = false
        }
        val f = q.pollFirst()
        if (f == null) {
            buffering = true
            underruns++
            target = minOf(maxTarget, target + 1)
            stableFrames = 0
            return null
        }
        if (++stableFrames > 500 && target > minTarget) { // 10 s without underrun: give back 20 ms
            target--
            stableFrames = 0
        }
        return f
    }

    @Synchronized
    fun size() = q.size

    @Synchronized
    fun currentTarget() = target

    @Synchronized
    fun clear() {
        q.clear()
        buffering = true
    }
}

package io.github.revocx35.webipphone.phone

import android.Manifest
import android.annotation.SuppressLint
import android.content.Context
import android.content.pm.PackageManager
import android.media.AudioAttributes
import android.media.AudioFormat
import android.media.AudioRecord
import android.media.AudioTrack
import android.media.MediaRecorder
import android.media.audiofx.AcousticEchoCanceler
import android.media.audiofx.AutomaticGainControl
import android.media.audiofx.NoiseSuppressor
import android.os.Process
import android.util.Log
import androidx.core.content.ContextCompat
import io.github.revocx35.webipphone.BuildConfig
import kotlin.math.sqrt
import kotlinx.coroutines.flow.MutableStateFlow

/**
 * Microphone -> 20 ms G.711 frames -> [send]; frames from the server -> jitter buffer -> speaker.
 * Uses the voice-communication audio source/usage, so the platform echo canceller applies.
 */
class CallAudio(private val context: Context, private val send: (ByteArray) -> Boolean) {
    @Volatile var muted = false
    @Volatile private var alaw = false
    @Volatile private var running = false
    private val jitter = JitterBuffer()
    private var captureThread: Thread? = null
    private var playbackThread: Thread? = null

    /** Microphone level 0..1 (for the UI). */
    val level = MutableStateFlow(0f)

    /** Test hook for the emulator e2e: counts frames each way. */
    @Volatile var framesSent = 0L
        private set
    @Volatile var framesPlayed = 0L
        private set
    @Volatile var loudFramesPlayed = 0L
        private set

    val isRunning get() = running

    @Synchronized
    fun start(codec: String) {
        alaw = codec == "PCMA"
        if (running) return
        running = true
        jitter.clear()
        captureThread = Thread(::captureLoop, "wip-capture").apply { start() }
        playbackThread = Thread(::playbackLoop, "wip-playback").apply { start() }
    }

    fun setCodec(codec: String) {
        alaw = codec == "PCMA"
    }

    @Synchronized
    fun stop() {
        running = false
        captureThread?.interrupt()
        playbackThread?.interrupt()
        captureThread = null
        playbackThread = null
        jitter.clear()
        level.value = 0f
    }

    /** A frame from the server (without the 1-byte frame type). */
    fun play(payload: ByteArray) {
        if (running) jitter.push(payload)
    }

    @SuppressLint("MissingPermission") // checked below
    private fun openRecorder(): Pair<AudioRecord, Int>? {
        if (ContextCompat.checkSelfPermission(context, Manifest.permission.RECORD_AUDIO) != PackageManager.PERMISSION_GRANTED) {
            Log.w(TAG, "no microphone permission: receive-only call")
            return null
        }
        for (rate in intArrayOf(8000, 16000, 48000)) {
            val min = AudioRecord.getMinBufferSize(rate, AudioFormat.CHANNEL_IN_MONO, AudioFormat.ENCODING_PCM_16BIT)
            if (min <= 0) continue
            val rec = runCatching {
                AudioRecord(MediaRecorder.AudioSource.VOICE_COMMUNICATION, rate, AudioFormat.CHANNEL_IN_MONO,
                    AudioFormat.ENCODING_PCM_16BIT, maxOf(min, rate / 50 * 2 * 4))
            }.getOrNull() ?: continue
            if (rec.state == AudioRecord.STATE_INITIALIZED) return rec to rate
            rec.release()
        }
        return null
    }

    /** Debug builds only: replace the microphone with a 1 kHz tone (emulator tests). */
    @Volatile var testTone = false

    private fun toneLoop() {
        val frame = ShortArray(160)
        var phase = 0.0
        var next = System.nanoTime()
        while (running && !Thread.currentThread().isInterrupted) {
            for (i in frame.indices) {
                frame[i] = (8000 * kotlin.math.sin(phase)).toInt().toShort()
                phase += 2 * Math.PI * 1000 / 8000
            }
            emit(frame)
            next += 20_000_000
            val sleep = (next - System.nanoTime()) / 1_000_000
            if (sleep > 0) try { Thread.sleep(sleep) } catch (_: InterruptedException) { return }
        }
    }

    private fun captureLoop() {
        Process.setThreadPriority(Process.THREAD_PRIORITY_URGENT_AUDIO)
        if (BuildConfig.TEST_HOOKS && testTone) return toneLoop()
        val (rec, rate) = openRecorder() ?: return
        val effects = listOfNotNull(
            if (AcousticEchoCanceler.isAvailable()) AcousticEchoCanceler.create(rec.audioSessionId)?.apply { enabled = true } else null,
            if (NoiseSuppressor.isAvailable()) NoiseSuppressor.create(rec.audioSessionId)?.apply { enabled = true } else null,
            if (AutomaticGainControl.isAvailable()) AutomaticGainControl.create(rec.audioSessionId)?.apply { enabled = true } else null,
        )
        val factor = rate / 8000
        val dec = Decimator(factor)
        val input = ShortArray(160 * factor)
        val pcm = ShortArray(160 * factor)
        val frame = ShortArray(160)
        var n8k = 0
        try {
            rec.startRecording()
            while (running && !Thread.currentThread().isInterrupted) {
                val n = rec.read(input, 0, input.size)
                if (n <= 0) {
                    if (n < 0) break
                    continue
                }
                val k = dec.process(input, n, pcm)
                var i = 0
                while (i < k) {
                    val take = minOf(160 - n8k, k - i)
                    System.arraycopy(pcm, i, frame, n8k, take)
                    n8k += take
                    i += take
                    if (n8k == 160) {
                        n8k = 0
                        emit(frame)
                    }
                }
            }
        } catch (e: Exception) {
            Log.w(TAG, "capture stopped", e)
        } finally {
            runCatching { rec.stop() }
            effects.forEach { runCatching { it.release() } }
            rec.release()
        }
    }

    private fun emit(frame: ShortArray) {
        var sum = 0.0
        for (s in frame) sum += s.toDouble() * s
        level.value = (sqrt(sum / frame.size) / 32768).toFloat()
        if (muted) return
        val out = ByteArray(161)
        out[0] = FRAME_AUDIO
        G711.encode(frame, alaw, out, 1)
        if (send(out)) framesSent++
    }

    private fun playbackLoop() {
        Process.setThreadPriority(Process.THREAD_PRIORITY_URGENT_AUDIO)
        val min = AudioTrack.getMinBufferSize(8000, AudioFormat.CHANNEL_OUT_MONO, AudioFormat.ENCODING_PCM_16BIT)
        val track = runCatching {
            AudioTrack.Builder()
                .setAudioAttributes(AudioAttributes.Builder()
                    .setUsage(AudioAttributes.USAGE_VOICE_COMMUNICATION)
                    .setContentType(AudioAttributes.CONTENT_TYPE_SPEECH).build())
                .setAudioFormat(AudioFormat.Builder().setSampleRate(8000)
                    .setChannelMask(AudioFormat.CHANNEL_OUT_MONO)
                    .setEncoding(AudioFormat.ENCODING_PCM_16BIT).build())
                .setBufferSizeInBytes(maxOf(min, 320 * 3))
                .setTransferMode(AudioTrack.MODE_STREAM)
                .build()
        }.getOrElse {
            Log.e(TAG, "cannot open AudioTrack", it)
            return
        }
        val pcm = ShortArray(160)
        try {
            track.play()
            var lastLog = System.currentTimeMillis()
            while (running && !Thread.currentThread().isInterrupted) {
                if (BuildConfig.TEST_HOOKS && System.currentTimeMillis() - lastLog > 2000) {
                    lastLog = System.currentTimeMillis()
                    Log.i(TAG, "stats sent=$framesSent played=$framesPlayed loud=$loudFramesPlayed jitter=${jitter.size()}/${jitter.currentTarget()}")
                }
                val f = jitter.pop()
                if (f == null) {
                    pcm.fill(0)
                } else {
                    G711.decode(f, 0, minOf(f.size, 160), alaw, pcm)
                    if (f.size < 160) pcm.fill(0, f.size, 160)
                    framesPlayed++
                    var sum = 0.0
                    for (s in pcm) sum += s.toDouble() * s
                    if (sqrt(sum / 160) > 800) loudFramesPlayed++
                }
                // Blocking write: paces the loop at real time once the track buffer is full.
                if (track.write(pcm, 0, 160) < 0) break
            }
        } catch (e: Exception) {
            Log.w(TAG, "playback stopped", e)
        } finally {
            runCatching { track.stop() }
            track.release()
        }
    }

    companion object {
        const val FRAME_AUDIO: Byte = 1
        private const val TAG = "CallAudio"
    }
}

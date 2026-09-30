package io.github.revocx35.webipphone.phone

import android.content.Context
import android.media.AudioAttributes
import android.media.AudioManager
import android.media.Ringtone
import android.media.RingtoneManager
import android.media.ToneGenerator
import android.os.Build
import android.os.VibrationEffect
import android.os.Vibrator
import android.os.VibratorManager

/** Ringtone + vibration for incoming calls (respecting the phone's ringer mode), and the
 *  local ringback tone while an outgoing call rings without early media. */
class Ringer(private val context: Context) {
    private val am = context.getSystemService(AudioManager::class.java)
    private var ringtone: Ringtone? = null
    private var vibrating = false
    private var ringback: ToneGenerator? = null

    private val vibrator: Vibrator? get() = if (Build.VERSION.SDK_INT >= 31) {
        context.getSystemService(VibratorManager::class.java)?.defaultVibrator
    } else {
        @Suppress("DEPRECATION") context.getSystemService(Vibrator::class.java)
    }

    fun ring() {
        if (ringtone != null || vibrating) return
        val mode = am.ringerMode
        if (mode == AudioManager.RINGER_MODE_NORMAL) {
            val uri = RingtoneManager.getActualDefaultRingtoneUri(context, RingtoneManager.TYPE_RINGTONE)
                ?: RingtoneManager.getDefaultUri(RingtoneManager.TYPE_RINGTONE)
            ringtone = RingtoneManager.getRingtone(context, uri)?.apply {
                audioAttributes = AudioAttributes.Builder().setUsage(AudioAttributes.USAGE_NOTIFICATION_RINGTONE)
                    .setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION).build()
                if (Build.VERSION.SDK_INT >= 28) isLooping = true
                play()
            }
        }
        if (mode != AudioManager.RINGER_MODE_SILENT) {
            vibrator?.let {
                it.vibrate(VibrationEffect.createWaveform(longArrayOf(0, 800, 1200), 0), AudioAttributes.Builder()
                    .setUsage(AudioAttributes.USAGE_NOTIFICATION_RINGTONE).build())
                vibrating = true
            }
        }
    }

    fun stopRinging() {
        ringtone?.stop()
        ringtone = null
        if (vibrating) vibrator?.cancel()
        vibrating = false
    }

    fun startRingback() {
        if (ringback != null) return
        ringback = runCatching { ToneGenerator(AudioManager.STREAM_VOICE_CALL, 60) }.getOrNull()
            ?.apply { startTone(ToneGenerator.TONE_SUP_RINGTONE) }
    }

    fun stopRingback() {
        ringback?.apply { stopTone(); release() }
        ringback = null
    }

    private var keys: ToneGenerator? = null

    fun keyTone(digit: Char) {
        val tone = when (digit) {
            in '0'..'9' -> ToneGenerator.TONE_DTMF_0 + (digit - '0')
            '*' -> ToneGenerator.TONE_DTMF_S
            '#' -> ToneGenerator.TONE_DTMF_P
            else -> return
        }
        if (keys == null) keys = runCatching { ToneGenerator(AudioManager.STREAM_DTMF, 50) }.getOrNull()
        keys?.startTone(tone, 120)
    }
}

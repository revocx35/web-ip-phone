package io.github.revocx35.webipphone.phone

import android.Manifest
import android.annotation.SuppressLint
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.app.Person
import androidx.core.content.ContextCompat
import io.github.revocx35.webipphone.IncomingCallActivity
import io.github.revocx35.webipphone.MainActivity
import io.github.revocx35.webipphone.R
import io.github.revocx35.webipphone.net.CallView

object Notifications {
    const val CH_SERVICE = "service"
    const val CH_INCOMING = "incoming"
    const val CH_CALL = "call"
    const val CH_MISSED = "missed"
    const val ID_SERVICE = 1
    const val ID_INCOMING = 2
    private const val ID_MISSED_BASE = 1000

    fun createChannels(ctx: Context) {
        val nm = ctx.getSystemService(NotificationManager::class.java)
        nm.createNotificationChannels(listOf(
            NotificationChannel(CH_SERVICE, "Ready for calls", NotificationManager.IMPORTANCE_MIN).apply {
                description = "Shown while the app keeps the connection for incoming calls"
                setShowBadge(false)
            },
            NotificationChannel(CH_INCOMING, "Incoming calls", NotificationManager.IMPORTANCE_HIGH).apply {
                description = "Rings for incoming calls"
                setSound(null, null) // the app plays the ringtone itself so it stops when answered elsewhere
                enableVibration(false)
                lockscreenVisibility = Notification.VISIBILITY_PUBLIC
            },
            NotificationChannel(CH_CALL, "Ongoing call", NotificationManager.IMPORTANCE_LOW).apply { setShowBadge(false) },
            NotificationChannel(CH_MISSED, "Missed calls", NotificationManager.IMPORTANCE_DEFAULT),
        ))
    }

    private fun openApp(ctx: Context) = PendingIntent.getActivity(ctx, 0,
        Intent(ctx, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP),
        PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)

    private fun action(ctx: Context, action: String, callId: String, code: Int) = PendingIntent.getBroadcast(ctx, code,
        Intent(ctx, CallActionReceiver::class.java).setAction(action).putExtra(CallActionReceiver.EXTRA_CALL, callId),
        PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)

    private fun person(call: CallView) = Person.Builder().setName(call.who.ifBlank { "Unknown" }).setImportant(true).build()

    fun ready(ctx: Context, text: String): Notification = NotificationCompat.Builder(ctx, CH_SERVICE)
        .setSmallIcon(R.drawable.ic_stat_phone)
        .setContentTitle("Web IP Phone")
        .setContentText(text)
        .setContentIntent(openApp(ctx))
        .setOngoing(true)
        .setPriority(NotificationCompat.PRIORITY_MIN)
        .setCategory(NotificationCompat.CATEGORY_SERVICE)
        .build()

    fun incoming(ctx: Context, call: CallView, line: String): Notification {
        val full = PendingIntent.getActivity(ctx, 1,
            Intent(ctx, IncomingCallActivity::class.java).putExtra(IncomingCallActivity.EXTRA_CALL, call.id)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_NO_USER_ACTION),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        // Answer opens an activity (not a broadcast) so the app is in the foreground and may use the microphone.
        val answer = PendingIntent.getActivity(ctx, 2,
            Intent(ctx, IncomingCallActivity::class.java).putExtra(IncomingCallActivity.EXTRA_CALL, call.id)
                .putExtra(IncomingCallActivity.EXTRA_ANSWER, true).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        val decline = action(ctx, CallActionReceiver.ACTION_DECLINE, call.id, 3)
        return NotificationCompat.Builder(ctx, CH_INCOMING)
            .setSmallIcon(R.drawable.ic_stat_phone)
            .setContentTitle(call.who)
            .setContentText(if (call.remoteName.isNotBlank()) "${call.remote} · $line" else line)
            .setStyle(NotificationCompat.CallStyle.forIncomingCall(person(call), decline, answer))
            .setFullScreenIntent(full, true)
            .setContentIntent(full)
            .setCategory(NotificationCompat.CATEGORY_CALL)
            .setPriority(NotificationCompat.PRIORITY_MAX)
            .setVisibility(NotificationCompat.VISIBILITY_PUBLIC)
            .setOngoing(true)
            .build()
    }

    fun ongoing(ctx: Context, call: CallView): Notification {
        val b = NotificationCompat.Builder(ctx, CH_CALL)
            .setSmallIcon(R.drawable.ic_stat_phone)
            .setContentTitle(call.who)
            .setContentIntent(openApp(ctx))
            .setStyle(NotificationCompat.CallStyle.forOngoingCall(person(call), action(ctx, CallActionReceiver.ACTION_HANGUP, call.id, 4)))
            .setCategory(NotificationCompat.CATEGORY_CALL)
            .setOngoing(true)
        val answered = call.answeredAt?.let { runCatching { java.time.Instant.parse(it).toEpochMilli() }.getOrNull() }
        if (answered != null) b.setWhen(answered).setUsesChronometer(true).setShowWhen(true)
        else b.setContentText(if (call.direction == "out") "Calling…" else "Connecting…")
        return b.build()
    }

    /** Android 13+: posting needs the runtime permission (asked on the main screen). */
    private fun canNotify(ctx: Context) = Build.VERSION.SDK_INT < 33 ||
        ContextCompat.checkSelfPermission(ctx, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED

    @SuppressLint("MissingPermission") // guarded by canNotify()
    fun showIncoming(ctx: Context, call: CallView, line: String) {
        if (canNotify(ctx)) runCatching { NotificationManagerCompat.from(ctx).notify(ID_INCOMING, incoming(ctx, call, line)) }
    }

    fun cancelIncoming(ctx: Context) = NotificationManagerCompat.from(ctx).cancel(ID_INCOMING)

    @SuppressLint("MissingPermission") // guarded by canNotify()
    fun missed(ctx: Context, call: CallView) {
        val n = NotificationCompat.Builder(ctx, CH_MISSED)
            .setSmallIcon(R.drawable.ic_stat_phone)
            .setContentTitle("Missed call")
            .setContentText(if (call.remoteName.isNotBlank()) "${call.remoteName} (${call.remote})" else call.remote)
            .setContentIntent(openApp(ctx))
            .setCategory(NotificationCompat.CATEGORY_MISSED_CALL)
            .setAutoCancel(true)
            .build()
        if (canNotify(ctx)) runCatching { NotificationManagerCompat.from(ctx).notify(ID_MISSED_BASE + (call.id.hashCode() and 0xffff), n) }
    }
}

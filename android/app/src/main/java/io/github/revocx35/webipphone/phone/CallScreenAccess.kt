package io.github.revocx35.webipphone.phone

import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.os.Build
import android.provider.Settings
import androidx.core.net.toUri

/**
 * How an incoming call can reach the screen while the app is in the background:
 *  - Full-screen notification (Android 14+: special access "Full screen notifications", not granted
 *    by default to apps installed outside the Play Store): the system turns the screen on and shows
 *    the call screen over the lock screen.
 *  - Fallback "Display over other apps": lets the app start its call screen itself (background
 *    activity starts are otherwise blocked), also when the screen is turned on or unlocked later.
 */
object CallScreenAccess {
    fun fullScreenAllowed(ctx: Context): Boolean =
        Build.VERSION.SDK_INT < 34 || ctx.getSystemService(NotificationManager::class.java).canUseFullScreenIntent()

    fun overlayAllowed(ctx: Context): Boolean = Settings.canDrawOverlays(ctx)

    /** Neither way works: calls only show up as a notification. */
    fun limited(ctx: Context): Boolean = !fullScreenAllowed(ctx) && !overlayAllowed(ctx)

    fun fullScreenSettings(ctx: Context): Intent =
        if (Build.VERSION.SDK_INT >= 34) Intent(Settings.ACTION_MANAGE_APP_USE_FULL_SCREEN_INTENT, "package:${ctx.packageName}".toUri())
        else Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, "package:${ctx.packageName}".toUri())

    fun overlaySettings(ctx: Context): Intent =
        Intent(Settings.ACTION_MANAGE_OVERLAY_PERMISSION, "package:${ctx.packageName}".toUri())
}

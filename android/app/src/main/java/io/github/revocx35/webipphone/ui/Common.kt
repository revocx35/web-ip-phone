package io.github.revocx35.webipphone.ui

import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.content.ContextCompat
import java.net.InetAddress
import java.time.Duration
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle

object LocalNetwork {
    const val PERMISSION = "android.permission.ACCESS_LOCAL_NETWORK" // API 37 SDK constant

    fun granted(ctx: Context) = Build.VERSION.SDK_INT < 37 ||
        ContextCompat.checkSelfPermission(ctx, PERMISSION) == PackageManager.PERMISSION_GRANTED

    /** Whether the host is on the LAN (then Android 17 needs the permission). Call off the main thread. */
    fun isLan(host: String): Boolean = runCatching {
        InetAddress.getAllByName(host.trim('[', ']')).any { it.isSiteLocalAddress || it.isLinkLocalAddress || it.isLoopbackAddress ||
            (it.address.size == 16 && (it.address[0].toInt() and 0xfe) == 0xfc) }
    }.getOrDefault(host.endsWith(".local"))
}

fun hasPermission(ctx: Context, p: String) = ContextCompat.checkSelfPermission(ctx, p) == PackageManager.PERMISSION_GRANTED

fun openAppSettings(ctx: Context) {
    ctx.startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", ctx.packageName, null))
        .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
}

fun formatDuration(seconds: Long): String {
    val s = maxOf(0, seconds)
    val h = s / 3600
    val m = (s % 3600) / 60
    return if (h > 0) "%d:%02d:%02d".format(h, m, s % 60) else "%d:%02d".format(m, s % 60)
}

fun parseInstant(s: String?): Instant? = s?.let { runCatching { Instant.parse(it) }.getOrNull() }

fun formatWhen(s: String): String {
    val t = parseInstant(s) ?: return ""
    val local = t.atZone(ZoneId.systemDefault())
    val today = Instant.now().atZone(ZoneId.systemDefault()).toLocalDate()
    return if (local.toLocalDate() == today) local.format(DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT))
    else local.format(DateTimeFormatter.ofLocalizedDateTime(FormatStyle.SHORT))
}

fun secondsSince(s: String?): Long = parseInstant(s)?.let { Duration.between(it, Instant.now()).seconds } ?: 0

@Composable
fun Avatar(name: String, size: Dp = 96.dp) {
    val letters = name.split(' ').filter { w -> w.any { it.isLetter() } }.take(2).joinToString("") { it.first().uppercase() }
    Surface(shape = CircleShape, color = MaterialTheme.colorScheme.primaryContainer, modifier = Modifier.size(size)) {
        Box(contentAlignment = Alignment.Center) {
            Text(letters.ifEmpty { "#" }, fontSize = (size.value / 2.6).sp, fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.onPrimaryContainer)
        }
    }
}

@Composable
fun CenteredColumn(modifier: Modifier = Modifier, content: @Composable () -> Unit) {
    Column(modifier.fillMaxWidth().padding(24.dp), horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(14.dp)) { content() }
}

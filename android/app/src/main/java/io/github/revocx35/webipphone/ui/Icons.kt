package io.github.revocx35.webipphone.ui

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.PathParser
import androidx.compose.ui.unit.dp

/** Stroke icons (same drawings as the web UI), so no icon library is needed. */
object AppIcons {
    private fun icon(name: String, vararg paths: String, fill: Boolean = false): ImageVector {
        val b = ImageVector.Builder(name, 24.dp, 24.dp, 24f, 24f)
        for (p in paths) {
            val nodes = PathParser().parsePathString(p).toNodes()
            if (fill) b.addPath(nodes, fill = SolidColor(Color.Black))
            else b.addPath(nodes, stroke = SolidColor(Color.Black), strokeLineWidth = 2f, strokeLineCap = StrokeCap.Round, strokeLineJoin = StrokeJoin.Round)
        }
        return b.build()
    }

    val Phone = icon("phone", "M22 16.9v3a2 2 0 0 1-2.2 2 19.8 19.8 0 0 1-8.6-3.1 19.5 19.5 0 0 1-6-6A19.8 19.8 0 0 1 2.1 4.2 2 2 0 0 1 4.1 2h3a2 2 0 0 1 2 1.7c.1 1 .4 1.9.7 2.8a2 2 0 0 1-.5 2.1L8 9.9a16 16 0 0 0 6 6l1.3-1.3a2 2 0 0 1 2.1-.4c.9.3 1.8.6 2.8.7a2 2 0 0 1 1.7 2z")
    val PhoneFilled = icon("phone-filled", "M6.6 10.8c1.4 2.8 3.8 5.1 6.6 6.6l2.2-2.2c.3-.3.7-.4 1-.2 1.1.4 2.3.6 3.6.6.6 0 1 .4 1 1V20c0 .6-.4 1-1 1-9.4 0-17-7.6-17-17 0-.6.4-1 1-1h3.5c.6 0 1 .4 1 1 0 1.3.2 2.5.6 3.6.1.3 0 .7-.2 1L6.6 10.8z", fill = true)
    val Grid = icon("grid", "M5 4.5a.5.5 0 1 0 0 1 .5.5 0 1 0 0-1M12 4.5a.5.5 0 1 0 0 1 .5.5 0 1 0 0-1M19 4.5a.5.5 0 1 0 0 1 .5.5 0 1 0 0-1M5 11.5a.5.5 0 1 0 0 1 .5.5 0 1 0 0-1M12 11.5a.5.5 0 1 0 0 1 .5.5 0 1 0 0-1M19 11.5a.5.5 0 1 0 0 1 .5.5 0 1 0 0-1M5 18.5a.5.5 0 1 0 0 1 .5.5 0 1 0 0-1M12 18.5a.5.5 0 1 0 0 1 .5.5 0 1 0 0-1M19 18.5a.5.5 0 1 0 0 1 .5.5 0 1 0 0-1")
    val Clock = icon("clock", "M12 2a10 10 0 1 0 0 20 10 10 0 1 0 0-20", "M12 6v6l4 2")
    val Settings = icon("settings", "M12 9a3 3 0 1 0 0 6 3 3 0 1 0 0-6", "M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z")
    val Mic = icon("mic", "M12 2a3 3 0 0 0-3 3v6a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3z", "M19 10v1a7 7 0 0 1-14 0v-1M12 18v4M8 22h8")
    val MicOff = icon("mic-off", "M2 2l20 20M9 9v2a3 3 0 0 0 5.1 2.1M15 9.3V5a3 3 0 0 0-5.9-.6", "M17 16.9A7 7 0 0 1 5 11v-1M19 10v1c0 .8-.1 1.5-.4 2.2M12 18v4M8 22h8")
    val Pause = icon("pause", "M7 4h3v16H7zM14 4h3v16h-3z")
    val Speaker = icon("speaker", "M11 5L6 9H2v6h4l5 4V5zM15.5 8.5a5 5 0 0 1 0 7M19 5a10 10 0 0 1 0 14")
    val Transfer = icon("transfer", "M17 3l4 4-4 4M21 7H9M7 21l-4-4 4-4M3 17h12")
    val Backspace = icon("backspace", "M21 4H8l-7 8 7 8h13a2 2 0 0 0 2-2V6a2 2 0 0 0-2-2zM18 9l-6 6M12 9l6 6")
    val ArrowOut = icon("out", "M7 17L17 7M8 7h9v9")
    val ArrowIn = icon("in", "M17 7L7 17M16 17H7V8")
    val Contacts = icon("contacts", "M9 3a4 4 0 1 0 0 8 4 4 0 1 0 0-8", "M1 21a8 8 0 0 1 16 0M16 3.1a4 4 0 0 1 0 7.8M23 21a8 8 0 0 0-5-7.4")
    private const val STAR = "M12 2.5l2.9 6 6.6.9-4.8 4.6 1.2 6.5L12 17.4l-5.9 3.1 1.2-6.5-4.8-4.6 6.6-.9z"
    val Star = icon("star", STAR)
    val StarFilled = icon("star-filled", STAR, fill = true)
    val Edit = icon("edit", "M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z")
    val UserPlus = icon("user-plus", "M9 4a4 4 0 1 0 0 8 4 4 0 1 0 0-8", "M1 21a8 8 0 0 1 16 0M20 8v6M17 11h6")
    val Search = icon("search", "M11 4a7 7 0 1 0 0 14 7 7 0 1 0 0-14", "M21 21l-4.3-4.3")
    val Plus = icon("plus", "M12 5v14M5 12h14")
    val Close = icon("close", "M18 6L6 18M6 6l12 12")
}

fun Tab.icon(): ImageVector = when (this) {
    Tab.KEYPAD -> AppIcons.Grid
    Tab.RECENTS -> AppIcons.Clock
    Tab.CONTACTS -> AppIcons.Contacts
    Tab.SETTINGS -> AppIcons.Settings
}

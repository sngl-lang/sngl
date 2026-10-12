package PACKAGE

import android.content.Context
import android.graphics.BitmapFactory
import androidx.compose.foundation.Image
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap

// SnglAssets reads the pictures the program carries out of the app's assets,
// where the build put each file at the path the program names it by. A
// picture a URL names is not one of them: no loader ships with the app, so it
// is no picture at all. MainActivity hands it the application's context, since
// a drawing paints where there is none to ask.
object SnglAssets {
    private var context: Context? = null
    private val pictures = HashMap<String, ImageBitmap?>()

    fun init(c: Context) {
        context = c.applicationContext
    }

    fun bitmap(path: String): ImageBitmap? = pictures.getOrPut(path) {
        val c = context ?: return null
        if (path.startsWith("http://") || path.startsWith("https://")) {
            null
        } else {
            runCatching { c.assets.open(path.removePrefix("/")).use { BitmapFactory.decodeStream(it) } }
                .getOrNull()?.asImageBitmap()
        }
    }
}

// SnglAssetImage is an image's source on this platform: the picture a URL
// names, or nothing where there is none.
@Composable
fun SnglAssetImage(url: String) {
    val bmp = SnglAssets.bitmap(url) ?: return
    Image(bitmap = bmp, contentDescription = null, modifier = Modifier)
}

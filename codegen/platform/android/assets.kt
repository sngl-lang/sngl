package PACKAGE

import android.content.Context
import android.graphics.BitmapFactory
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Box
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import java.net.HttpURLConnection
import java.net.URL
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

// SnglAssets reads the pictures the program carries out of the app's assets,
// where the build put each file at the path the program names it by, and
// fetches the pictures a URL names. MainActivity hands it the application's
// context, since a drawing paints where there is none to ask.
object SnglAssets {
    private var context: Context? = null
    private val pictures = HashMap<String, ImageBitmap?>()
    private val fetched = ConcurrentHashMap<String, ImageBitmap>()

    // How many fetches are still running: a test waits for none before it
    // reads what their images reported.
    val active = AtomicInteger()

    fun init(c: Context) {
        context = c.applicationContext
    }

    fun bitmap(path: String): ImageBitmap? = pictures.getOrPut(path) {
        val c = context ?: return null
        if (isRemote(path)) {
            null
        } else {
            runCatching { c.assets.open(path.removePrefix("/")).use { BitmapFactory.decodeStream(it) } }
                .getOrNull()?.asImageBitmap()
        }
    }

    // Whether there are assets to read: a composition with no activity of
    // the app's own around it has none, and says nothing of a file.
    fun canRead(): Boolean = context != null

    fun isRemote(path: String): Boolean = path.startsWith("http://") || path.startsWith("https://")

    // The picture url names, once one has arrived.
    fun cached(url: String): ImageBitmap? = fetched[url]

    // Fetches the picture url names off the main thread. A picture that
    // arrives is kept for the life of the process; a failure is asked again
    // the next time an image shows the url.
    suspend fun fetch(url: String): Result<ImageBitmap> {
        fetched[url]?.let { return Result.success(it) }
        return withContext(Dispatchers.IO) {
            runCatching {
                val conn = URL(url).openConnection() as HttpURLConnection
                try {
                    conn.connectTimeout = 15_000
                    conn.readTimeout = 30_000
                    val status = conn.responseCode
                    if (status !in 200..299) throw java.io.IOException("HTTP $status")
                    val bmp = conn.inputStream.use { BitmapFactory.decodeStream(it) }
                        ?: throw java.io.IOException("not an image")
                    bmp.asImageBitmap()
                } finally {
                    conn.disconnect()
                }
            }
        }.onSuccess { fetched[url] = it }
    }
}

// What an image reports, handed to the source it holds: the source is what
// knows when its picture arrived.
class SnglImageEvents(val load: () -> Unit, val error: (String) -> Unit)

val LocalSnglImageEvents = staticCompositionLocalOf<SnglImageEvents?> { null }

// SnglImage is an image on this platform: a box holding its source, which
// reports to it.
@Composable
fun SnglImage(
    modifier: Modifier = Modifier,
    onLoad: () -> Unit = {},
    onError: (String) -> Unit = {},
    src: @Composable () -> Unit,
) {
    CompositionLocalProvider(LocalSnglImageEvents provides SnglImageEvents(onLoad, onError)) {
        Box(modifier = modifier) { src() }
    }
}

// SnglAssetImage is the picture a file the program carries holds, or nothing
// where the app's assets hold none.
@Composable
fun SnglAssetImage(url: String) {
    val events by rememberUpdatedState(LocalSnglImageEvents.current)
    val bmp = SnglAssets.bitmap(url)
    LaunchedEffect(url, bmp != null) {
        when {
            bmp != null -> events?.load?.invoke()
            SnglAssets.canRead() -> events?.error?.invoke("no picture at $url")
        }
    }
    bmp ?: return
    Image(bitmap = bmp, contentDescription = null, modifier = Modifier)
}

// SnglRemoteImage is the picture a URL names, fetched while the program runs:
// nothing until it arrives, and nothing if it does not.
@Composable
fun SnglRemoteImage(url: String) {
    val events by rememberUpdatedState(LocalSnglImageEvents.current)
    val bmp by produceState(SnglAssets.cached(url), url) {
        if (value != null) {
            events?.load?.invoke()
            return@produceState
        }
        SnglAssets.active.incrementAndGet()
        try {
            SnglAssets.fetch(url)
                .onSuccess {
                    value = it
                    events?.load?.invoke()
                }
                .onFailure { events?.error?.invoke(it.message ?: "could not load $url") }
        } finally {
            SnglAssets.active.decrementAndGet()
        }
    }
    val shown = bmp ?: return
    Image(bitmap = shown, contentDescription = null, modifier = Modifier)
}

package facets.mobile.management

import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext
import facets.mobile.FacetsApplication
import facets.mobile.data.cache.OfflineFacetsRepository

/** Uses the application Room database and the currently saved, validated server URL. */
@Composable
fun rememberConfiguredRepository(): OfflineFacetsRepository? {
    val context = LocalContext.current
    val application = context.applicationContext as? FacetsApplication ?: return null
    val baseUrl by application.settingsStore.baseUrl.collectAsState(initial = null)
    return remember(application, baseUrl) { baseUrl?.let { runCatching { application.repositoryFor(it) }.getOrNull() } }
}

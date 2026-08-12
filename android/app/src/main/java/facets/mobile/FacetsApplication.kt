package facets.mobile

import android.app.Application
import facets.mobile.data.cache.FacetsDatabase
import facets.mobile.data.cache.OfflineFacetsRepository
import facets.mobile.data.transport.FacetsClientFactory
import facets.mobile.data.transport.ServerUrlValidator
import facets.mobile.settings.ServerSettingsStore
import facets.mobile.sync.SyncScheduler
import facets.mobile.navigation.ProjectShortcutPublisher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.launch

class FacetsApplication : Application() {
    private val applicationScope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    val database: FacetsDatabase by lazy { FacetsDatabase.create(this) }
    val settingsStore: ServerSettingsStore by lazy {
        ServerSettingsStore(this) { raw -> runCatching { ServerUrlValidator.canonicalize(raw) } }
    }
    val syncScheduler: SyncScheduler by lazy { SyncScheduler(this, settingsStore) }
    private val shortcutPublisher by lazy { ProjectShortcutPublisher(this) }

    override fun onCreate() {
        super.onCreate()
        applicationScope.launch {
            database.projects().observeAll().collect { projects -> shortcutPublisher.publish(projects) }
        }
        syncScheduler.start(applicationScope)
    }

    fun repositoryFor(baseUrl: String): OfflineFacetsRepository = OfflineFacetsRepository(
        remote = FacetsClientFactory.create(baseUrl),
        database = database,
        onMutationSuccess = { syncScheduler.enqueueImmediate("mutation") },
    )
}

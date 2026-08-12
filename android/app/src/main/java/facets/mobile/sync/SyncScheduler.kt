package facets.mobile.sync

import android.content.Context
import androidx.work.BackoffPolicy
import androidx.work.Constraints
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import facets.mobile.settings.MIN_SYNC_INTERVAL_MINUTES
import facets.mobile.settings.ServerSettingsStore
import facets.mobile.settings.SyncSettings
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.launch

/** Owns the single periodic and single immediate synchronization chains. */
class SyncScheduler(
    context: Context,
    private val settingsStore: ServerSettingsStore,
) {
    private val context = context.applicationContext
    private val workManager = WorkManager.getInstance(this.context)

    fun start(scope: CoroutineScope) {
        scope.launch {
            combine(settingsStore.baseUrl, settingsStore.syncSettings) { url, settings -> url to settings }
                .distinctUntilChanged()
                .collect { (url, settings) ->
                    schedulePeriodic(url, settings)
                    if (url != null && settings.enabled) enqueueImmediate("configuration")
                }
        }
    }

    fun schedulePeriodic(baseUrl: String?, settings: SyncSettings) {
        if (baseUrl.isNullOrBlank() || !settings.enabled) {
            workManager.cancelUniqueWork(PERIODIC_WORK_NAME)
            return
        }
        val interval = settings.intervalMinutes.coerceAtLeast(MIN_SYNC_INTERVAL_MINUTES)
        val request = PeriodicWorkRequestBuilder<SyncWorker>(interval, TimeUnit.MINUTES)
            .setConstraints(networkConstraints)
            .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, BACKOFF_DELAY, TimeUnit.SECONDS)
            .build()
        workManager.enqueueUniquePeriodicWork(PERIODIC_WORK_NAME, ExistingPeriodicWorkPolicy.UPDATE, request)
    }

    fun rescheduleFromPreferences() {
        // A preference change is observed by [start]; this method is useful to callers that
        // update settings and want an immediate one-time refresh as well.
        enqueueImmediate("preferences")
    }

    fun enqueueImmediate(reason: String = "manual") {
        val request = OneTimeWorkRequestBuilder<SyncWorker>()
            .setConstraints(networkConstraints)
            .setInputData(androidx.work.workDataOf(REASON_KEY to reason))
            .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, BACKOFF_DELAY, TimeUnit.SECONDS)
            .build()
        workManager.enqueueUniqueWork(IMMEDIATE_WORK_NAME, ExistingWorkPolicy.KEEP, request)
    }

    fun enqueueWidgetRefresh() = enqueueImmediate("widget")

    fun cancel() {
        workManager.cancelUniqueWork(PERIODIC_WORK_NAME)
        workManager.cancelUniqueWork(IMMEDIATE_WORK_NAME)
    }

    fun notifyWidgets() {
        context.sendBroadcast(android.content.Intent(ACTION_SYNC_COMPLETED).setPackage(context.packageName))
    }

    private companion object {
        const val PERIODIC_WORK_NAME = "facets.periodic-sync"
        const val IMMEDIATE_WORK_NAME = "facets.immediate-sync"
        const val REASON_KEY = "reason"
        const val ACTION_SYNC_COMPLETED = "facets.mobile.sync.SYNC_COMPLETED"
        val BACKOFF_DELAY = 30L
        val networkConstraints = Constraints.Builder()
            .setRequiredNetworkType(NetworkType.CONNECTED)
            .build()
    }
}

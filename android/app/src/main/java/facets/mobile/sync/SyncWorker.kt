package facets.mobile.sync

import android.content.Context
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import facets.mobile.FacetsApplication
import facets.mobile.data.transport.ServerUrlValidator
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.first

class SyncWorker(
    appContext: Context,
    workerParams: WorkerParameters,
) : CoroutineWorker(appContext, workerParams) {
    override suspend fun doWork(): Result {
        val application = applicationContext as? FacetsApplication
            ?: return Result.failure(workDataOf(ERROR_KEY to "Application unavailable"))
        val baseUrl = application.settingsStore.baseUrl.first()
            ?: return Result.failure(workDataOf(ERROR_KEY to "Server not configured"))
        val syncSettings = application.settingsStore.syncSettings.first()
        val reason = inputData.getString(REASON_KEY)
        if (!syncSettings.enabled && reason !in setOf("manual", "widget")) {
            return Result.success()
        }
        val normalizedUrl = runCatching { ServerUrlValidator.canonicalize(baseUrl) }
            .getOrElse { return Result.failure(workDataOf(ERROR_KEY to "Invalid server address")) }

        return try {
            application.repositoryFor(normalizedUrl).refreshAll()
            application.syncScheduler.notifyWidgets()
            Result.success()
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (error: Throwable) {
            if (SyncFailureClassifier.shouldRetry(error)) Result.retry()
            else Result.failure(workDataOf(ERROR_KEY to (error.message ?: error::class.simpleName.orEmpty())))
        }
    }

    private companion object {
        const val ERROR_KEY = "error"
        const val REASON_KEY = "reason"
    }
}

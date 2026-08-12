package facets.mobile.sync

import facets.mobile.data.transport.FacetsException
import facets.mobile.data.transport.FacetsFailure
import java.io.IOException
import kotlinx.coroutines.CancellationException

/** WorkManager retry policy for failures returned by the typed transport layer. */
object SyncFailureClassifier {
    fun shouldRetry(error: Throwable): Boolean = when (val failure = (error as? FacetsException)?.failure) {
        FacetsFailure.NetworkOffline,
        FacetsFailure.Timeout -> true
        is FacetsFailure.ServerFailure -> failure.retryable
        FacetsFailure.Cancelled -> false
        is FacetsFailure.InvalidUrl,
        is FacetsFailure.InvalidRequest,
        is FacetsFailure.NotFound,
        is FacetsFailure.Conflict,
        is FacetsFailure.RemoteCancellation,
        is FacetsFailure.IncompatibleContract,
        is FacetsFailure.MalformedResponse -> false
        null -> error is IOException && error !is CancellationException
    }
}

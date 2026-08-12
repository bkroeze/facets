package facets.mobile.data.transport

import java.io.IOException

sealed interface FacetsFailure {
    data class InvalidUrl(val reason: String) : FacetsFailure

    data class InvalidRequest(
        val status: Int,
        val code: String? = null,
        val message: String? = null,
        val fields: Map<String, String> = emptyMap(),
    ) : FacetsFailure

    data class NotFound(
        val status: Int = 404,
        val code: String? = null,
        val message: String? = null,
    ) : FacetsFailure

    data class Conflict(
        val status: Int = 409,
        val code: String? = null,
        val message: String? = null,
    ) : FacetsFailure

    /** A remote request was canceled by the server (HTTP 408), distinct from local coroutine cancellation. */
    data class RemoteCancellation(
        val status: Int = 408,
        val code: String? = null,
        val message: String? = null,
    ) : FacetsFailure

    data class ServerFailure(
        val status: Int,
        val code: String? = null,
        val message: String? = null,
        val retryable: Boolean = status == 502 || status == 503 || status == 504,
    ) : FacetsFailure

    data object NetworkOffline : FacetsFailure
    data object Timeout : FacetsFailure
    data object Cancelled : FacetsFailure
    data class IncompatibleContract(val reason: String) : FacetsFailure
    data class MalformedResponse(val reason: String) : FacetsFailure
}

class FacetsException(
    val failure: FacetsFailure,
    cause: Throwable? = null,
) : IOException(failure.toString(), cause)

class InvalidServerUrlException(
    val failure: FacetsFailure.InvalidUrl,
) : IllegalArgumentException(failure.reason)

internal class UnknownApiEnumException(message: String) : kotlinx.serialization.SerializationException(message)
internal class MalformedResponseException(message: String, cause: Throwable? = null) : RuntimeException(message, cause)

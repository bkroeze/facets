package facets.mobile.data.transport

import facets.mobile.data.remote.FacetsApi
import facets.mobile.data.remote.facetsJson
import okhttp3.HttpUrl
import okhttp3.OkHttpClient
import okhttp3.MediaType.Companion.toMediaType
import java.util.concurrent.TimeUnit
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

object ServerUrlValidator {
    /** Validate and canonicalize the user-entered Tailnet HTTPS origin. */
    fun validate(raw: String): HttpUrl {
        val value = raw.trim()
        val url = value.toHttpUrlOrNull()
            ?: throw InvalidServerUrlException(FacetsFailure.InvalidUrl("Enter a valid HTTPS server URL"))
        if (url.scheme != "https") {
            throw InvalidServerUrlException(FacetsFailure.InvalidUrl("The server URL must use HTTPS"))
        }
        if (url.encodedUsername.isNotEmpty() || url.encodedPassword.isNotEmpty()) {
            throw InvalidServerUrlException(FacetsFailure.InvalidUrl("The server URL must not contain credentials"))
        }
        if (url.encodedPath != "/") {
            throw InvalidServerUrlException(FacetsFailure.InvalidUrl("The server URL must not contain a path"))
        }
        if (url.query != null || url.fragment != null) {
            throw InvalidServerUrlException(FacetsFailure.InvalidUrl("The server URL must not contain a query or fragment"))
        }
        return url
    }

    fun canonicalize(raw: String): String = validate(raw).toString().removeSuffix("/")
}

class FacetsClient internal constructor(
    private val repository: FacetsRepository,
) : FacetsRepository by repository

object FacetsClientFactory {
    private const val DEFAULT_CONNECT_TIMEOUT_MILLIS = 10_000L
    private const val DEFAULT_READ_TIMEOUT_MILLIS = 30_000L
    private const val DEFAULT_WRITE_TIMEOUT_MILLIS = 30_000L

    /** Build a client with finite timeouts and OkHttp's default certificate/hostname verification. */
    fun create(
        baseUrl: String,
        connectTimeoutMillis: Long = DEFAULT_CONNECT_TIMEOUT_MILLIS,
        readTimeoutMillis: Long = DEFAULT_READ_TIMEOUT_MILLIS,
        writeTimeoutMillis: Long = DEFAULT_WRITE_TIMEOUT_MILLIS,
    ): FacetsClient = createInternal(
        baseUrl = baseUrl,
        connectTimeoutMillis = connectTimeoutMillis,
        readTimeoutMillis = readTimeoutMillis,
        writeTimeoutMillis = writeTimeoutMillis,
        suppliedClient = null,
    )

    /**
     * HTTPS-only test seam. Production callers use [create], which always builds the
     * platform-verified OkHttp client.
     */
    internal fun createForTesting(
        baseUrl: String,
        connectTimeoutMillis: Long = DEFAULT_CONNECT_TIMEOUT_MILLIS,
        readTimeoutMillis: Long = DEFAULT_READ_TIMEOUT_MILLIS,
        writeTimeoutMillis: Long = DEFAULT_WRITE_TIMEOUT_MILLIS,
        httpClient: OkHttpClient,
    ): FacetsClient = createInternal(
        baseUrl = baseUrl,
        connectTimeoutMillis = connectTimeoutMillis,
        readTimeoutMillis = readTimeoutMillis,
        writeTimeoutMillis = writeTimeoutMillis,
        suppliedClient = httpClient,
    )

    private fun createInternal(
        baseUrl: String,
        connectTimeoutMillis: Long,
        readTimeoutMillis: Long,
        writeTimeoutMillis: Long,
        suppliedClient: OkHttpClient?,
    ): FacetsClient {
        val validated = ServerUrlValidator.validate(baseUrl)
        requireTimeout("connect", connectTimeoutMillis)
        requireTimeout("read", readTimeoutMillis)
        requireTimeout("write", writeTimeoutMillis)
        val baseClient = (suppliedClient ?: OkHttpClient()).newBuilder()
            .connectTimeout(connectTimeoutMillis, TimeUnit.MILLISECONDS)
            .readTimeout(readTimeoutMillis, TimeUnit.MILLISECONDS)
            .writeTimeout(writeTimeoutMillis, TimeUnit.MILLISECONDS)
            .followRedirects(false)
            .followSslRedirects(false)
            .build()
        val client = baseClient.newBuilder()
            .addInterceptor { chain ->
                val request = chain.request().newBuilder()
                    .header("Accept", "application/json")
                    .build()
                chain.proceed(request)
            }
            // No logging interceptor: task bodies, evidence, and URLs never enter release logs.
            .build()
        val retrofit = Retrofit.Builder()
            .baseUrl(validated.toString())
            .client(client)
            .addConverterFactory(facetsJson.asConverterFactory("application/json".toMediaType()))
            .build()
        return FacetsClient(RetrofitFacetsRepository(retrofit.create(FacetsApi::class.java)))
    }

    private fun requireTimeout(name: String, value: Long) {
        require(value in 1..MAX_TIMEOUT_MILLIS) { "$name timeout must be finite and between 1ms and ${MAX_TIMEOUT_MILLIS}ms" }
    }

    private const val MAX_TIMEOUT_MILLIS = 10 * 60 * 1000L
}

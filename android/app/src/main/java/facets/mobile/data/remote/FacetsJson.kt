package facets.mobile.data.remote

import kotlinx.serialization.json.Json

/** Shared strict request/response JSON configuration. */
val facetsJson: Json = Json {
    ignoreUnknownKeys = true
    isLenient = false
    coerceInputValues = false
    explicitNulls = true
    encodeDefaults = false
}

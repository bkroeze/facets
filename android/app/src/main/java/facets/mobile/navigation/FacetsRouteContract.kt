package facets.mobile.navigation

import android.net.Uri

sealed interface FacetsDestination {
    data class Project(val projectId: String) : FacetsDestination
    data class Task(val projectId: String, val taskId: String) : FacetsDestination
}

sealed interface FacetsRouteParseResult {
    data class Valid(val destination: FacetsDestination) : FacetsRouteParseResult
    data class Invalid(val message: String) : FacetsRouteParseResult
}

/** The single external-entry contract for projects and tasks. */
object FacetsRouteContract {
    const val SCHEME = "facets"
    const val HOST = "projects"
    const val TASKS_SEGMENT = "tasks"
    const val MAX_ID_LENGTH = 256

    fun parse(uri: Uri?): FacetsRouteParseResult = parse(uri?.toString())

    /** JVM-testable parser used by intent handling and Android URI parsing. */
    fun parse(rawUri: String?): FacetsRouteParseResult {
        if (rawUri == null) return FacetsRouteParseResult.Invalid("This link is missing a target.")
        val uri = runCatching { java.net.URI(rawUri) }.getOrNull()
            ?: return FacetsRouteParseResult.Invalid("This link has an invalid path.")
        if (!uri.isAbsolute || !uri.scheme.equals(SCHEME, ignoreCase = true) || uri.rawAuthority != HOST) {
            return FacetsRouteParseResult.Invalid("This is not a Facets project link.")
        }
        if (uri.rawQuery != null || uri.rawFragment != null) {
            return FacetsRouteParseResult.Invalid("Facets links cannot include a query or fragment.")
        }
        val rawSegments = uri.rawPath.removePrefix("/").split("/")
        val segments = runCatching {
            rawSegments.map { java.net.URLDecoder.decode(it, "UTF-8") }
        }.getOrNull() ?: return FacetsRouteParseResult.Invalid("This link has an invalid path.")
        val destination = when (segments.size) {
            1 -> if (isSafeOpaqueId(segments[0])) FacetsDestination.Project(segments[0]) else null
            3 -> if (segments[1] == TASKS_SEGMENT && isSafeOpaqueId(segments[0]) && isSafeOpaqueId(segments[2])) {
                FacetsDestination.Task(segments[0], segments[2])
            } else null
            else -> null
        }
        return destination?.let(FacetsRouteParseResult::Valid)
            ?: FacetsRouteParseResult.Invalid("This link has an invalid project or task ID.")
    }

    fun isSafeOpaqueId(id: String): Boolean = id.isNotEmpty() &&
        id.length <= MAX_ID_LENGTH &&
        id != "." &&
        id != ".." &&
        id.none { character ->
            character == '/' || character == '\\' || character == '?' || character == '#' || character.isISOControl()
        }

    fun projectUri(projectId: String): Uri = Uri.Builder()
        .scheme(SCHEME)
        .authority(HOST)
        .appendPath(projectId)
        .build()

    fun taskUri(projectId: String, taskId: String): Uri = Uri.Builder()
        .scheme(SCHEME)
        .authority(HOST)
        .appendPath(projectId)
        .appendPath(TASKS_SEGMENT)
        .appendPath(taskId)
        .build()

    fun projectNavigationRoute(projectId: String): String = "projects/${encodeSegment(projectId)}"

    fun taskNavigationRoute(projectId: String, taskId: String): String =
        "projects/${encodeSegment(projectId)}/tasks/${encodeSegment(taskId)}"

    private fun encodeSegment(value: String): String =
        java.net.URLEncoder.encode(value, "UTF-8").replace("+", "%20")
}

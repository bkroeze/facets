package facets.mobile.data.transport

import facets.mobile.data.model.CloseTaskRequest
import facets.mobile.data.model.CommentRequest
import facets.mobile.data.model.CreateTaskRequest
import facets.mobile.data.model.CreateViewRequest
import facets.mobile.data.model.HealthStatus
import facets.mobile.data.model.Project
import facets.mobile.data.model.SavedView
import facets.mobile.data.model.Task
import facets.mobile.data.model.TaskUpdateField
import facets.mobile.data.model.ViewUpdateField
import facets.mobile.data.model.TaskListStatus
import facets.mobile.data.model.UpdateTaskRequest
import facets.mobile.data.model.UpdateViewRequest
import facets.mobile.data.remote.ErrorEnvelopeDto
import facets.mobile.data.remote.FacetsApi
import facets.mobile.data.remote.toDomain
import facets.mobile.data.remote.toWire
import facets.mobile.data.remote.facetsJson
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import retrofit2.Response
import java.io.IOException
import java.io.InterruptedIOException
import java.net.SocketException
import java.net.SocketTimeoutException
import java.net.UnknownHostException

interface FacetsRepository {
    suspend fun apiVersion(): String
    suspend fun checkHealth(): HealthStatus
    suspend fun listProjects(): List<Project>
    suspend fun getProject(projectId: String): Project
    suspend fun listTasks(projectId: String, viewId: String? = null, status: TaskListStatus? = null): List<Task>
    suspend fun getTask(projectId: String, taskId: String): Task
    suspend fun createTask(projectId: String, request: CreateTaskRequest): Task
    suspend fun updateTask(projectId: String, taskId: String, request: UpdateTaskRequest): Task
    suspend fun commentTask(projectId: String, taskId: String, request: CommentRequest): Task
    suspend fun closeTask(projectId: String, taskId: String, request: CloseTaskRequest): Task
    suspend fun reopenTask(projectId: String, taskId: String): Task
    suspend fun deleteTask(projectId: String, taskId: String)
    suspend fun listViews(): List<SavedView>
    suspend fun createView(request: CreateViewRequest): SavedView
    suspend fun getView(viewId: String): SavedView
    suspend fun updateView(viewId: String, request: UpdateViewRequest): SavedView
    suspend fun deleteView(viewId: String)
    suspend fun listTasksForView(projectId: String, viewId: String): List<Task>
}

class RetrofitFacetsRepository(
    private val api: FacetsApi,
    private val json: Json = facetsJson,
) : FacetsRepository {
    override suspend fun checkHealth(): HealthStatus {
        val response = try {
            api.health()
        } catch (error: CancellationException) {
            throw error
        } catch (error: IOException) {
            currentCoroutineContext().ensureActive()
            throw FacetsException(networkFailure(error), error)
        }
        if (!response.isSuccessful) throw FacetsException(errorFailure(response))
        response.body()?.close()
        return HealthStatus(healthy = true)
    }

    override suspend fun apiVersion(): String = execute(api::apiRoot) { root ->
        if (root.version != API_VERSION) {
            throw FacetsException(FacetsFailure.IncompatibleContract("Expected API version $API_VERSION, got ${root.version}"))
        }
        root.version
    }

    override suspend fun listProjects(): List<Project> = execute(api::listProjects) { it.projects.map { project -> project.toDomain() } }
    override suspend fun getProject(projectId: String): Project = execute({ api.getProject(projectId) }, { it.toDomain() })

    override suspend fun listTasks(projectId: String, viewId: String?, status: TaskListStatus?): List<Task> {
        if (viewId != null && status != null) {
            throw localInvalidRequest("view and status selectors are mutually exclusive")
        }
        val statusValue = status?.name?.lowercase()
        return execute({ api.listTasks(projectId, viewId, statusValue) }) { it.tasks.map { task -> task.toDomain() } }
    }

    override suspend fun getTask(projectId: String, taskId: String): Task = execute({ api.getTask(projectId, taskId) }, { it.toDomain() })
    override suspend fun createTask(projectId: String, request: CreateTaskRequest): Task = execute({ api.createTask(projectId, request.toWire()) }, { it.toDomain() })

    override suspend fun updateTask(projectId: String, taskId: String, request: UpdateTaskRequest): Task {
        if (request.presentFields().isEmpty()) {
            throw localInvalidRequest("at least one task field is required")
        }
        validateTaskPatch(request)
        return execute({ api.updateTask(projectId, taskId, request.toWire()) }, { it.toDomain() })
    }

    override suspend fun commentTask(projectId: String, taskId: String, request: CommentRequest): Task =
        execute({ api.commentTask(projectId, taskId, request.toWire()) }, { it.toDomain() })

    override suspend fun closeTask(projectId: String, taskId: String, request: CloseTaskRequest): Task =
        execute({ api.closeTask(projectId, taskId, request.toWire()) }, { it.toDomain() })

    override suspend fun reopenTask(projectId: String, taskId: String): Task =
        execute({ api.reopenTask(projectId, taskId) }, { it.toDomain() })

    override suspend fun deleteTask(projectId: String, taskId: String) {
        executeUnit { api.deleteTask(projectId, taskId, facets.mobile.data.remote.DeleteTaskDto(taskId)) }
    }

    override suspend fun listViews(): List<SavedView> = execute(api::listViews) { it.views.map { view -> view.toDomain() } }
    override suspend fun createView(request: CreateViewRequest): SavedView = execute({ api.createView(request.toWire()) }, { it.toDomain() })
    override suspend fun getView(viewId: String): SavedView = execute({ api.getView(viewId) }, { it.toDomain() })

    override suspend fun updateView(viewId: String, request: UpdateViewRequest): SavedView {
        if (request.presentFields().isEmpty()) {
            throw localInvalidRequest("at least one saved-view field is required")
        }
        validateViewPatch(request)
        return execute({ api.updateView(viewId, request.toWire()) }, { it.toDomain() })
    }

    override suspend fun deleteView(viewId: String) {
        executeUnit { api.deleteView(viewId) }
    }

    override suspend fun listTasksForView(projectId: String, viewId: String): List<Task> =
        execute({ api.listTasksForView(projectId, viewId) }) { it.tasks.map { task -> task.toDomain() } }
    private fun validateTaskPatch(request: UpdateTaskRequest) {
        if (TaskUpdateField.TITLE in request.fields && request.title == null) {
            throw localInvalidRequest("title cannot be null")
        }
        if (TaskUpdateField.DESCRIPTION in request.fields && request.description == null) {
            throw localInvalidRequest("description must be an empty string when cleared")
        }
        if (TaskUpdateField.ASSIGNEE in request.fields && request.assignee == null) {
            throw localInvalidRequest("assignee must be an empty string when cleared")
        }
    }

    private fun validateViewPatch(request: UpdateViewRequest) {
        if (ViewUpdateField.NAME in request.fields && request.name == null) {
            throw localInvalidRequest("name cannot be null")
        }
        if (ViewUpdateField.QUERY in request.fields && request.query == null) {
            throw localInvalidRequest("query cannot be null")
        }
        if (ViewUpdateField.ORDER in request.fields && request.order == null) {
            throw localInvalidRequest("order cannot be null")
        }
    }


    private suspend fun <T, R> execute(call: suspend () -> Response<T>, transform: (T) -> R): R {
        val response = try {
            call()
        } catch (error: CancellationException) {
            throw error
        } catch (error: UnknownApiEnumException) {
            throw FacetsException(FacetsFailure.IncompatibleContract(error.message ?: "Unknown API enum"), error)
        } catch (error: SerializationException) {
            throw FacetsException(serializationFailure(error), error)
        } catch (error: IOException) {
            currentCoroutineContext().ensureActive()
            throw FacetsException(networkFailure(error), error)
        }
        if (!response.isSuccessful) throw FacetsException(errorFailure(response))
        val body = response.body() ?: throw FacetsException(FacetsFailure.MalformedResponse("Successful response had no body"))
        return try {
            transform(body)
        } catch (error: FacetsException) {
            throw error
        } catch (error: UnknownApiEnumException) {
            throw FacetsException(FacetsFailure.IncompatibleContract(error.message ?: "Unknown API enum"), error)
        } catch (error: SerializationException) {
            throw FacetsException(serializationFailure(error), error)
        } catch (error: MalformedResponseException) {
            throw FacetsException(FacetsFailure.MalformedResponse(error.message ?: "Malformed response"), error)
        } catch (error: DateTimeExceptionCompat) {
            throw FacetsException(FacetsFailure.MalformedResponse(error.message ?: "Malformed response"), error)
        }
    }
    private suspend fun executeUnit(call: suspend () -> Response<Unit>) {
        val response = try {
            call()
        } catch (error: CancellationException) {
            throw error
        } catch (error: IOException) {
            currentCoroutineContext().ensureActive()
            throw FacetsException(networkFailure(error), error)
        }
        if (!response.isSuccessful) throw FacetsException(errorFailure(response))
    }

    private fun errorFailure(response: Response<*>): FacetsFailure {
        val errorBody = response.errorBody()
        val rawError = errorBody?.let {
            try {
                it.string()
            } finally {
                it.close()
            }
        }?.takeIf { it.isNotBlank() }
        val envelope = rawError?.let { raw ->
            try {
                json.decodeFromString<ErrorEnvelopeDto>(raw)
            } catch (error: CancellationException) {
                throw error
            } catch (error: UnknownApiEnumException) {
                throw FacetsException(FacetsFailure.IncompatibleContract(error.message ?: "Unknown API error code"), error)
            } catch (error: SerializationException) {
                throw FacetsException(serializationFailure(error), error)
            }
        }
        val error = envelope?.error
        val code = error?.code?.wireValue
        val message = error?.message
        val fields = error?.details?.fields.orEmpty()
        return when (response.code()) {
            404 -> FacetsFailure.NotFound(response.code(), code, message)
            409 -> FacetsFailure.Conflict(response.code(), code, message)
            408 -> FacetsFailure.RemoteCancellation(response.code(), code, message)
            in 400..499 -> FacetsFailure.InvalidRequest(response.code(), code, message, fields)
            else -> FacetsFailure.ServerFailure(response.code(), code, message)
        }
    }

    private fun serializationFailure(error: SerializationException): FacetsFailure {
        var cause: Throwable? = error
        while (cause != null) {
            if (cause is UnknownApiEnumException) {
                return FacetsFailure.IncompatibleContract(cause.message ?: "Unknown API enum")
            }
            cause = cause.cause
        }
        return FacetsFailure.MalformedResponse("Response JSON could not be decoded")
    }
    private fun networkFailure(error: IOException): FacetsFailure {
        var cause: Throwable? = error
        while (cause != null) {
            if (cause is SocketTimeoutException || cause is InterruptedIOException) {
                return FacetsFailure.Timeout
            }
            cause = cause.cause
        }
        return FacetsFailure.NetworkOffline
    }

    private fun localInvalidRequest(message: String): FacetsException =
        FacetsException(FacetsFailure.InvalidRequest(400, "invalid_request", message))

    private companion object {
        const val API_VERSION = "v1"
    }
}

/** Marker used to keep timestamp conversion failures typed without exposing java.time internals. */
private typealias DateTimeExceptionCompat = IllegalArgumentException

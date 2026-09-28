package facets.mobile.data.cache

import androidx.room.withTransaction
import facets.mobile.data.model.CloseTaskRequest
import facets.mobile.data.model.CommentRequest
import facets.mobile.data.model.CreateTaskRequest
import facets.mobile.data.model.CreateViewRequest
import facets.mobile.data.model.HealthStatus
import facets.mobile.data.model.Project
import facets.mobile.data.model.SavedView
import facets.mobile.data.model.Task
import facets.mobile.data.model.TaskListStatus
import facets.mobile.data.model.UpdateTaskRequest
import facets.mobile.data.model.UpdateViewRequest
import facets.mobile.data.transport.FacetsFailure
import facets.mobile.data.transport.FacetsException
import facets.mobile.data.transport.FacetsRepository
import java.time.Instant
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

/** State for the most recent completed refresh. A failure never changes lastSuccess. */
data class CacheSyncState(
    val scope: String,
    val lastAttempt: Instant?,
    val lastSuccess: Instant?,
    val lastError: String?,
)

data class RefreshSummary(
    val projects: Int,
    val tasks: Int,
    val views: Int,
)

/** Room-backed repository. REST is used only to refresh or perform a connected mutation. */
class OfflineFacetsRepository(
    private val remote: FacetsRepository,
    private val database: FacetsDatabase,
    private val onMutationSuccess: (() -> Unit)? = null,
) : FacetsRepository {
    private val projects get() = database.projects()
    private val tasks get() = database.tasks()
    private val views get() = database.views()
    private val syncState get() = database.syncState()

    fun observeProjects(): Flow<List<Project>> = projects.observeAll().map { rows -> rows.map(ProjectEntity::toDomain) }

    fun observeTasks(projectId: String): Flow<List<Task>> =
        tasks.observeForProject(projectId).map { rows -> rows.map(TaskEntity::toDomain) }

    fun observeViews(): Flow<List<SavedView>> = views.observeAll().map { rows -> rows.map(SavedViewEntity::toDomain) }

    suspend fun syncState(scope: String): CacheSyncState? = syncState.get(scope)?.toDomain()

    fun observeSyncState(scope: String): Flow<CacheSyncState?> =
        syncState.observe(scope).map { it?.toDomain() }

    override suspend fun apiVersion(): String = remote.apiVersion()

    override suspend fun checkHealth(): HealthStatus = remote.checkHealth()
    override suspend fun getToday() = remote.getToday()

    override suspend fun setTodayFocus(text: String) = remote.setTodayFocus(text)

    /** Read methods use the last successful Room snapshot and do not perform network I/O. */
    override suspend fun listProjects(): List<Project> = projects.getAll().map(ProjectEntity::toDomain)

    override suspend fun getProject(projectId: String): Project =
        projects.get(projectId)?.toDomain() ?: remote.getProject(projectId).also { cacheProject(it) }

    override suspend fun listTasks(projectId: String, viewId: String?, status: TaskListStatus?): List<Task> {
        var result = tasks.getForProject(projectId).map(TaskEntity::toDomain)
        if (status != null && status != TaskListStatus.ALL) {
            result = result.filter { it.status.name == status.name }
        }
        if (viewId != null) {
            val view = views.get(viewId)?.toDomain()
            if (view != null) result = applyView(result, view)
        }
        return result
    }

    override suspend fun getTask(projectId: String, taskId: String): Task =
        tasks.get(taskId)?.takeIf { it.projectId == projectId }?.toDomain()
            ?: remote.getTask(projectId, taskId).also { cacheTask(it) }

    override suspend fun createTask(projectId: String, request: CreateTaskRequest): Task =
        runMutation(FacetsDatabase.projectTasksScope(projectId), { remote.createTask(projectId, request) }) { task ->
            tasks.upsert(task.toEntity())
        }

    override suspend fun updateTask(projectId: String, taskId: String, request: UpdateTaskRequest): Task =
        runMutation(FacetsDatabase.projectTasksScope(projectId), { remote.updateTask(projectId, taskId, request) }) { task ->
            tasks.upsert(task.toEntity())
        }

    override suspend fun commentTask(projectId: String, taskId: String, request: CommentRequest): Task =
        runMutation(FacetsDatabase.projectTasksScope(projectId), { remote.commentTask(projectId, taskId, request) }) { task ->
            tasks.upsert(task.toEntity())
        }

    override suspend fun closeTask(projectId: String, taskId: String, request: CloseTaskRequest): Task =
        runMutation(FacetsDatabase.projectTasksScope(projectId), { remote.closeTask(projectId, taskId, request) }) { task ->
            tasks.upsert(task.toEntity())
        }

    override suspend fun reopenTask(projectId: String, taskId: String): Task =
        runMutation(FacetsDatabase.projectTasksScope(projectId), { remote.reopenTask(projectId, taskId) }) { task ->
            tasks.upsert(task.toEntity())
        }

    override suspend fun deleteTask(projectId: String, taskId: String) {
        runMutation(FacetsDatabase.projectTasksScope(projectId), { remote.deleteTask(projectId, taskId) }) {
            tasks.delete(taskId)
        }
    }

    override suspend fun listViews(): List<SavedView> = views.getAll().map(SavedViewEntity::toDomain)

    override suspend fun createView(request: CreateViewRequest): SavedView =
        runMutation(FacetsDatabase.VIEWS_SCOPE, { remote.createView(request) }) { view ->
            views.upsert(view.toEntity())
        }

    override suspend fun getView(viewId: String): SavedView =
        views.get(viewId)?.toDomain() ?: remote.getView(viewId).also { cacheView(it) }

    override suspend fun updateView(viewId: String, request: UpdateViewRequest): SavedView =
        runMutation(FacetsDatabase.VIEWS_SCOPE, { remote.updateView(viewId, request) }) { view ->
            views.upsert(view.toEntity())
        }

    override suspend fun deleteView(viewId: String) {
        runMutation(FacetsDatabase.VIEWS_SCOPE, { remote.deleteView(viewId) }) {
            views.delete(viewId)
        }
    }

    override suspend fun listTasksForView(projectId: String, viewId: String): List<Task> =
        listTasks(projectId, viewId, TaskListStatus.ALL)

    /** Replace only after the complete projects response has succeeded. */
    suspend fun refreshProjects(configuredProjectIds: Set<String>? = null): List<Project> =
        refresh(FacetsDatabase.PROJECTS_SCOPE) {
            val snapshot = remote.listProjects()
            val retained = configuredProjectIds?.let { ids -> snapshot.filter { it.id in ids } } ?: snapshot
            database.withTransaction {
                if (retained.isEmpty()) projects.deleteAll() else projects.deleteAbsent(retained.map(Project::id))
                projects.upsertAll(retained.map(Project::toEntity))
                markSuccessInTransaction(FacetsDatabase.PROJECTS_SCOPE)
            }
            retained
        }

    /** Fetches project and its complete task snapshot, then commits both atomically. */
    suspend fun refreshProject(projectId: String): Project = refresh(FacetsDatabase.projectTasksScope(projectId)) {
        val project = remote.getProject(projectId)
        val snapshot = remote.listTasks(projectId, viewId = null, status = null)
        database.withTransaction {
            projects.upsertAll(listOf(project.toEntity()))
            tasks.deleteForProject(projectId)
            tasks.upsertAll(snapshot.filter { it.projectId == projectId }.map(Task::toEntity))
            markSuccessInTransaction(FacetsDatabase.projectTasksScope(projectId))
        }
        project
    }

    /** A task response without a view/status filter is a complete project snapshot. */
    suspend fun refreshTasks(projectId: String): List<Task> = refresh(FacetsDatabase.projectTasksScope(projectId)) {
        val snapshot = remote.listTasks(projectId, viewId = null, status = null)
        database.withTransaction {
            tasks.deleteForProject(projectId)
            tasks.upsertAll(snapshot.filter { it.projectId == projectId }.map(Task::toEntity))
            markSuccessInTransaction(FacetsDatabase.projectTasksScope(projectId))
        }
        snapshot.filter { it.projectId == projectId }
    }

    /** Replace only after the complete views response has succeeded. */
    suspend fun refreshViews(configuredViewIds: Set<String>? = null): List<SavedView> = refresh(FacetsDatabase.VIEWS_SCOPE) {
        val snapshot = remote.listViews()
        database.withTransaction {
            val retained = configuredViewIds?.let { ids -> snapshot.filter { it.id in ids } } ?: snapshot
            if (retained.isEmpty()) views.deleteAll() else views.deleteAbsent(retained.map(SavedView::id))
            views.upsertAll(retained.map(SavedView::toEntity))
            markSuccessInTransaction(FacetsDatabase.VIEWS_SCOPE)
        }
        configuredViewIds?.let { ids -> snapshot.filter { it.id in ids } } ?: snapshot
    }

    /** Refreshes every configured snapshot while retaining each prior committed snapshot on failure. */
    suspend fun refreshAll(): RefreshSummary = refresh(FacetsDatabase.SYNC_SCOPE) {
        val refreshedProjects = refreshProjects()
        val refreshedViews = refreshViews()
        var taskCount = 0
        refreshedProjects.forEach { project ->
            taskCount += refreshTasks(project.id).size
        }
        database.withTransaction { markSuccessInTransaction(FacetsDatabase.SYNC_SCOPE) }
        RefreshSummary(refreshedProjects.size, taskCount, refreshedViews.size)
    }

    private suspend fun cacheProject(project: Project) {
        database.withTransaction { projects.upsertAll(listOf(project.toEntity())) }
    }

    private suspend fun cacheTask(task: Task) {
        database.withTransaction { tasks.upsert(task.toEntity()) }
    }

    private suspend fun cacheView(view: SavedView) {
        database.withTransaction { views.upsert(view.toEntity()) }
    }
    private suspend fun <T> runMutation(
        scope: String,
        operation: suspend () -> T,
        apply: suspend (T) -> Unit,
    ): T = try {
        markAttempt(scope)
        val result = operation()
        database.withTransaction {
            apply(result)
            markSuccessInTransaction(scope)
        }
        onMutationSuccess?.invoke()
        result
    } catch (cancelled: CancellationException) {
        throw cancelled
    } catch (error: Throwable) {
        recordFailure(scope, error)
        throw error
    }
    private suspend fun <T> refresh(scope: String, operation: suspend () -> T): T = try {
        markAttempt(scope)
        operation()
    } catch (cancelled: CancellationException) {
        throw cancelled
    } catch (error: Throwable) {
        recordFailure(scope, error)
        throw error
    }

    private suspend fun markAttempt(scope: String) {
        database.withTransaction {
            val previous = syncState.get(scope)
            syncState.upsert(
                SyncStateEntity(scope, Instant.now().toEpochMilli(), previous?.lastSuccessEpochMillis, previous?.lastError),
            )
        }
    }

    private suspend fun recordFailure(scope: String, error: Throwable) {
        database.withTransaction {
            val previous = syncState.get(scope)
            syncState.upsert(
                SyncStateEntity(
                    scope,
                    previous?.lastAttemptEpochMillis,
                    previous?.lastSuccessEpochMillis,
                    safeFailureMessage(error),
                ),
            )
        }
    }

    private suspend fun markSuccessInTransaction(scope: String) {
        val previous = syncState.get(scope)
        syncState.upsert(SyncStateEntity(scope, previous?.lastAttemptEpochMillis, Instant.now().toEpochMilli(), null))
    }

    private fun safeFailureMessage(error: Throwable): String = when (val failure = (error as? FacetsException)?.failure) {
        FacetsFailure.NetworkOffline -> "Offline"
        FacetsFailure.Timeout -> "Timed out"
        is FacetsFailure.ServerFailure -> if (failure.retryable) "Server unavailable" else "Server error"
        is FacetsFailure.InvalidUrl -> "Invalid server address"
        is FacetsFailure.InvalidRequest -> "Request rejected"
        is FacetsFailure.NotFound -> "Resource not found"
        is FacetsFailure.Conflict -> "Conflict"
        is FacetsFailure.RemoteCancellation -> "Server canceled request"
        is FacetsFailure.IncompatibleContract -> "Incompatible server"
        is FacetsFailure.MalformedResponse -> "Invalid server response"
        FacetsFailure.Cancelled -> "Canceled"
        null -> error::class.simpleName ?: "Sync failed"
    }


    private fun applyView(tasks: List<Task>, view: SavedView): List<Task> {
        val filtered = tasks.filter { task ->
            (view.query.statuses.isEmpty() || task.status in view.query.statuses) &&
                (view.query.assignees.isEmpty() || task.assignee in view.query.assignees) &&
                (view.query.priorities.isEmpty() || task.priority in view.query.priorities)
        }
        return when (view.order.field) {
            facets.mobile.data.model.ViewOrderField.TITLE -> filtered.sortedBy { it.title }
            facets.mobile.data.model.ViewOrderField.STATUS -> filtered.sortedBy { it.status.name }
            facets.mobile.data.model.ViewOrderField.PRIORITY -> filtered.sortedBy { it.priority }
            facets.mobile.data.model.ViewOrderField.ASSIGNEE -> filtered.sortedBy { it.assignee }
            facets.mobile.data.model.ViewOrderField.CREATED_AT -> filtered.sortedBy { it.createdAt }
            facets.mobile.data.model.ViewOrderField.UPDATED_AT -> filtered.sortedBy { it.updatedAt }
        }.let { if (view.order.direction == facets.mobile.data.model.ViewOrderDirection.DESC) it.asReversed() else it }
    }
}

private fun SyncStateEntity.toDomain() = CacheSyncState(
    scope = scope,
    lastAttempt = lastAttemptEpochMillis?.let(Instant::ofEpochMilli),
    lastSuccess = lastSuccessEpochMillis?.let(Instant::ofEpochMilli),
    lastError = lastError,
)

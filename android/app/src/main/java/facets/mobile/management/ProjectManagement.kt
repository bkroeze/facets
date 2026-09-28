@file:OptIn(ExperimentalMaterial3Api::class)
package facets.mobile.management
import android.net.Uri
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.ArrowBack
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import facets.mobile.data.cache.OfflineFacetsRepository
import facets.mobile.data.model.CloseTaskRequest
import facets.mobile.data.model.CommentRequest
import facets.mobile.data.model.CreateTaskRequest
import facets.mobile.data.model.SavedView
import facets.mobile.data.model.Task
import facets.mobile.data.model.TaskStatus
import facets.mobile.data.model.TodayFocus
import facets.mobile.data.model.TodaySnapshot
import facets.mobile.data.model.UpdateTaskRequest
import facets.mobile.data.model.ViewOrderDirection
import facets.mobile.data.model.ViewOrderField
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import java.time.Duration
import java.time.Instant

private const val STALE_AFTER_MINUTES = 15L

data class ProjectListUiState(val projects: List<facets.mobile.data.model.Project> = emptyList(), val loading: Boolean = true, val refreshing: Boolean = false, val error: String? = null, val stale: Boolean = false)
data class ProjectDetailUiState(val project: facets.mobile.data.model.Project? = null, val tasks: List<Task> = emptyList(), val views: List<SavedView> = emptyList(), val activeViewId: String? = null, val loading: Boolean = true, val refreshing: Boolean = false, val error: String? = null, val stale: Boolean = false)
data class TodayUiState(
    val snapshot: TodaySnapshot? = null,
    val loading: Boolean = true,
    val refreshing: Boolean = false,
    val error: String? = null,
)

/** Coordinates Room-backed reads and connected mutations while retaining cached data on failure. */
class ProjectManagementController(private val repository: OfflineFacetsRepository, private val scope: CoroutineScope) {
    private val _projects = MutableStateFlow(ProjectListUiState())
    val projects: StateFlow<ProjectListUiState> = _projects.asStateFlow()
    private val _detail = MutableStateFlow(ProjectDetailUiState())
    val detail: StateFlow<ProjectDetailUiState> = _detail.asStateFlow()
    private val _today = MutableStateFlow(TodayUiState())
    val today: StateFlow<TodayUiState> = _today.asStateFlow()
    private var projectJob: Job? = null
    private var tasksJob: Job? = null
    private var allTasks: List<Task> = emptyList()
    private var currentProjectId: String? = null

    init {
        scope.launch { repository.observeProjects().collect { value -> _projects.value = _projects.value.copy(projects = value, loading = false) } }
        scope.launch { repository.observeViews().collect { value -> _detail.value = _detail.value.copy(views = value, activeViewId = _detail.value.activeViewId ?: value.firstOrNull()?.id); recomputeTasks() } }
        refreshProjects()
        refreshToday()
    }

    fun selectProject(projectId: String) {
        if (currentProjectId == projectId) return
        currentProjectId = projectId
        tasksJob?.cancel()
        projectJob?.cancel()
        _detail.value = ProjectDetailUiState(
            project = _projects.value.projects.firstOrNull { it.id == projectId },
            views = _detail.value.views,
            activeViewId = _detail.value.activeViewId ?: _detail.value.views.firstOrNull()?.id,
        )
        projectJob = scope.launch {
            repository.observeProjects().collect { list ->
                _detail.value = _detail.value.copy(project = list.firstOrNull { it.id == projectId })
            }
        }
        tasksJob = scope.launch {
            repository.observeTasks(projectId).collect { value ->
                allTasks = value
                _detail.value = _detail.value.copy(loading = false, tasks = filteredTasks())
            }
        }
        refreshProject(projectId)
    }

    fun selectView(viewId: String?) {
        _detail.value = _detail.value.copy(activeViewId = viewId, tasks = filteredTasks(viewId))
    }

    fun refreshProjects() {
        if (_projects.value.refreshing) return
        scope.launch {
            _projects.value = _projects.value.copy(refreshing = true, error = null)
            runCatching { repository.refreshProjects() }
                .onFailure { _projects.value = _projects.value.copy(error = readableError(it)) }
            _projects.value = _projects.value.copy(refreshing = false, stale = isStale("projects"))
        }
    }

    fun refreshProject(projectId: String? = currentProjectId) {
        val id = projectId ?: return
        if (_detail.value.refreshing) return
        scope.launch {
            _detail.value = _detail.value.copy(refreshing = true, error = null)
            runCatching { repository.refreshProject(id) }
                .onFailure { _detail.value = _detail.value.copy(error = readableError(it)) }
            _detail.value = _detail.value.copy(refreshing = false, stale = isStale("tasks:$id"))
        }
    }

    fun refreshToday() {
        if (_today.value.refreshing) return
        _today.value = _today.value.copy(refreshing = true, error = null)
        scope.launch {
            runCatching { repository.getToday() }
                .onSuccess { _today.value = TodayUiState(snapshot = it) }
                .onFailure { _today.value = _today.value.copy(loading = false, refreshing = false, error = readableError(it)) }
        }
    }

    suspend fun setTodayFocus(text: String): Result<TodayFocus> = runCatching {
        repository.setTodayFocus(text.trim()).also { focus ->
            _today.value = _today.value.copy(
                snapshot = _today.value.snapshot?.copy(focus = focus),
                error = null,
            )
        }
    }

    suspend fun createTask(projectId: String, request: CreateTaskRequest): Result<Task> = mutate { repository.createTask(projectId, request) }
    suspend fun toggleTopTask(projectId: String, task: Task): Result<Task> =
        updateTask(
            projectId,
            task.id,
            UpdateTaskRequest(top = !task.top, fields = setOf(facets.mobile.data.model.TaskUpdateField.TOP)),
        ).also { result ->
            if (result.isSuccess) refreshToday()
        }
    suspend fun updateTask(projectId: String, taskId: String, request: UpdateTaskRequest): Result<Task> = mutate { repository.updateTask(projectId, taskId, request) }
    suspend fun commentTask(projectId: String, taskId: String, body: String): Result<Task> = mutate { repository.commentTask(projectId, taskId, CommentRequest(body)) }
    suspend fun closeTask(projectId: String, taskId: String, message: String, evidence: List<String>, comment: String?): Result<Task> = mutate { repository.closeTask(projectId, taskId, CloseTaskRequest(message, evidence, comment)) }
    suspend fun reopenTask(projectId: String, taskId: String): Result<Task> = mutate { repository.reopenTask(projectId, taskId) }
    suspend fun deleteTask(projectId: String, taskId: String): Result<Unit> = mutate { repository.deleteTask(projectId, taskId) }

    private suspend fun <T> mutate(operation: suspend () -> T): Result<T> = runCatching { operation() }

    private fun filteredTasks(viewId: String? = _detail.value.activeViewId): List<Task> {
        val view = _detail.value.views.firstOrNull { it.id == viewId } ?: return allTasks
        val filtered = allTasks.filter { task -> (view.query.statuses.isEmpty() || task.status in view.query.statuses) && (view.query.assignees.isEmpty() || task.assignee in view.query.assignees) && (view.query.priorities.isEmpty() || task.priority in view.query.priorities) }
        val sorted = when (view.order.field) { ViewOrderField.TITLE -> filtered.sortedBy { it.title.lowercase() }; ViewOrderField.STATUS -> filtered.sortedBy { it.status.name }; ViewOrderField.PRIORITY -> filtered.sortedBy { it.priority }; ViewOrderField.ASSIGNEE -> filtered.sortedBy { it.assignee.lowercase() }; ViewOrderField.CREATED_AT -> filtered.sortedBy { it.createdAt }; ViewOrderField.UPDATED_AT -> filtered.sortedBy { it.updatedAt } }
        return if (view.order.direction == ViewOrderDirection.DESC) sorted.asReversed() else sorted
    }

    private fun recomputeTasks() { _detail.value = _detail.value.copy(tasks = filteredTasks()) }
    private suspend fun isStale(scopeName: String): Boolean = repository.syncState(scopeName)?.lastSuccess?.let { Duration.between(it, Instant.now()).toMinutes() >= STALE_AFTER_MINUTES } ?: false
    private fun readableError(error: Throwable): String = error.message?.takeIf { it.isNotBlank() } ?: "Unable to update cached data. Check your connection and try again."
}

@Composable
fun ProjectManagementRoot(repository: OfflineFacetsRepository?, onOpenProject: (String) -> Unit, modifier: Modifier = Modifier) {
    if (repository == null) Column(modifier.fillMaxSize().padding(24.dp), verticalArrangement = Arrangement.Center) { Text("Connect a server in Settings to browse projects.", style = MaterialTheme.typography.titleMedium) }
    else { val scope = rememberCoroutineScope(); val controller = remember(repository, scope) { ProjectManagementController(repository, scope) }; ProjectListScreen(controller, onOpenProject, modifier) }
}

@Composable
fun ProjectListScreen(controller: ProjectManagementController, onOpenProject: (String) -> Unit, modifier: Modifier = Modifier) {
    val state by controller.projects.collectAsState()
    Scaffold(modifier = modifier, topBar = { TopAppBar(title = { Text("Projects") }, actions = { IconButton(onClick = controller::refreshProjects, enabled = !state.refreshing, modifier = Modifier.semantics { contentDescription = "Refresh projects" }) { Icon(Icons.Default.Refresh, contentDescription = null) } }) }) { padding ->
        Column(Modifier.padding(padding).fillMaxSize()) {
            if (state.refreshing) LinearProgressIndicator(Modifier.fillMaxWidth())
            state.error?.let { ErrorBanner(it, controller::refreshProjects) }
            if (state.stale && state.projects.isNotEmpty()) StaleBanner("Showing cached projects", controller::refreshProjects)
            when { state.loading -> LoadingState(); state.projects.isEmpty() && state.error == null -> EmptyState("No projects found"); state.projects.isEmpty() -> ErrorState(state.error ?: "No projects found", controller::refreshProjects); else -> LazyColumn(Modifier.fillMaxSize(), contentPadding = androidx.compose.foundation.layout.PaddingValues(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) { items(state.projects, key = { it.id }) { project -> Card(onClick = { onOpenProject(project.id) }, modifier = Modifier.fillMaxWidth().semantics { contentDescription = "Open project ${project.name}" }) { Column(Modifier.padding(16.dp)) { Text(project.name, style = MaterialTheme.typography.titleLarge); if (project.description.isNotBlank()) Text(project.description, maxLines = 2); Text("${project.activeTaskCount} active tasks", style = MaterialTheme.typography.labelLarge) } } } } }
        }
    }
}

@Composable
fun ProjectDetailScreen(
    controller: ProjectManagementController,
    projectId: String,
    onBack: () -> Unit,
    onOpenTask: (String, String) -> Unit,
    onCreateTask: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    LaunchedEffect(projectId) { controller.selectProject(projectId) }
    val state by controller.detail.collectAsState()
    val scope = rememberCoroutineScope()
    var viewMenuOpen by remember { mutableStateOf(false) }
    Scaffold(
        modifier = modifier,
        topBar = {
            TopAppBar(
                title = { Text(state.project?.name ?: "Project") },
                navigationIcon = { IconButton(onClick = onBack) { Icon(Icons.Default.ArrowBack, null) } },
                actions = {
                    IconButton(
                        onClick = { controller.refreshProject(projectId) },
                        enabled = !state.refreshing,
                        modifier = Modifier.semantics { contentDescription = "Refresh project tasks" },
                    ) { Icon(Icons.Default.Refresh, null) }
                },
            )
        },
        floatingActionButton = {
            ExtendedFloatingActionButton(
                onClick = { onCreateTask(projectId) },
                icon = { Icon(Icons.Default.Add, null) },
                text = { Text("Create task") },
                modifier = Modifier.semantics { contentDescription = "Create task" },
            )
        },
    ) { padding ->
        Column(Modifier.padding(padding).fillMaxSize()) {
            if (state.refreshing) LinearProgressIndicator(Modifier.fillMaxWidth())
            state.error?.let { ErrorBanner(it) { controller.refreshProject(projectId) } }
            if (state.stale) StaleBanner("Showing cached tasks") { controller.refreshProject(projectId) }
            Row(
                Modifier.fillMaxWidth().padding(horizontal = 12.dp),
                horizontalArrangement = Arrangement.SpaceBetween,
            ) {
                Text("${state.tasks.size} tasks", style = MaterialTheme.typography.titleMedium)
                Box {
                    TextButton(onClick = { viewMenuOpen = true }) {
                        Text(state.views.firstOrNull { it.id == state.activeViewId }?.name ?: "All tasks")
                    }
                    DropdownMenu(expanded = viewMenuOpen, onDismissRequest = { viewMenuOpen = false }) {
                        DropdownMenuItem(
                            text = { Text("All tasks") },
                            onClick = { controller.selectView(null); viewMenuOpen = false },
                        )
                        state.views.forEach { view ->
                            DropdownMenuItem(
                                text = { Text(view.name) },
                                onClick = { controller.selectView(view.id); viewMenuOpen = false },
                            )
                        }
                    }
                }
            }
            when {
                state.loading -> LoadingState()
                state.tasks.isEmpty() -> EmptyState("No tasks in this view")
                else -> LazyColumn(
                    Modifier.fillMaxSize(),
                    contentPadding = androidx.compose.foundation.layout.PaddingValues(12.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    items(state.tasks, key = { it.id }) { task ->
                        TaskRow(
                            task = task,
                            onClick = { onOpenTask(projectId, task.id) },
                            onToggleTop = {
                                scope.launch {
                                    controller.toggleTopTask(projectId, task)
                                }
                            },
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun TaskRow(task: Task, onClick: () -> Unit, onToggleTop: () -> Unit) {
    Card(
        onClick = onClick,
        modifier = Modifier.fillMaxWidth().semantics { contentDescription = "Open task ${task.title}" },
    ) {
        Row(
            Modifier.fillMaxWidth().padding(start = 16.dp, top = 12.dp, bottom = 12.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(task.title, style = MaterialTheme.typography.titleMedium)
                if (task.description.isNotBlank()) Text(task.description, maxLines = 2)
                Text(
                    "${task.status.name.lowercase().replaceFirstChar { it.uppercase() }} · ${task.assignee.ifBlank { "Unassigned" }}",
                    style = MaterialTheme.typography.labelMedium,
                )
            }
            TextButton(
                onClick = onToggleTop,
                modifier = Modifier.semantics {
                    contentDescription = if (task.top) "Remove ${task.title} from top tasks" else "Mark ${task.title} as a top task"
                },
            ) { Text(if (task.top) "★" else "☆") }
        }
    }
}

@Composable
fun TaskEditorScreen(initial: Task?, onBack: () -> Unit, onSave: suspend (CreateTaskRequest?, UpdateTaskRequest?) -> Result<Task>, modifier: Modifier = Modifier) {
    var title by remember(initial?.id) { mutableStateOf(initial?.title.orEmpty()) }; var description by remember(initial?.id) { mutableStateOf(initial?.description.orEmpty()) }; var priority by remember(initial?.id) { mutableStateOf(initial?.priority?.toString().orEmpty()) }; var assignee by remember(initial?.id) { mutableStateOf(initial?.assignee.orEmpty()) }; var submitting by remember { mutableStateOf(false) }; var error by remember { mutableStateOf<String?>(null) }; val scope = rememberCoroutineScope()
    Scaffold(modifier = modifier, topBar = { TopAppBar(title = { Text(if (initial == null) "Create task" else "Edit task") }, navigationIcon = { IconButton(onClick = onBack) { Icon(Icons.Default.ArrowBack, "Back") } }) }) { padding -> Column(Modifier.padding(padding).padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        OutlinedTextField(title, { title = it }, label = { Text("Title") }, isError = error != null && title.isBlank(), supportingText = { if (error != null && title.isBlank()) Text("Title is required") }, modifier = Modifier.fillMaxWidth(), singleLine = true); OutlinedTextField(description, { description = it }, label = { Text("Description") }, modifier = Modifier.fillMaxWidth(), minLines = 3); OutlinedTextField(priority, { priority = it.filter(Char::isDigit) }, label = { Text("Priority (1–5, optional)") }, isError = priority.toIntOrNull()?.let { it !in 1..5 } == true, keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number), modifier = Modifier.fillMaxWidth(), singleLine = true); OutlinedTextField(assignee, { assignee = it }, label = { Text("Assignee") }, modifier = Modifier.fillMaxWidth(), singleLine = true); error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
        Button(onClick = { val parsedPriority = priority.toIntOrNull(); if (title.isBlank()) { error = "Title is required"; return@Button }; if (priority.isNotBlank() && parsedPriority !in 1..5) { error = "Priority must be between 1 and 5"; return@Button }; submitting = true; error = null; scope.launch { val result = if (initial == null) onSave(CreateTaskRequest(title.trim(), description.trim().ifBlank { null }, parsedPriority, assignee.trim().ifBlank { null }), null) else onSave(null, UpdateTaskRequest(title.trim(), description.trim(), parsedPriority, assignee.trim(), fields = setOf(facets.mobile.data.model.TaskUpdateField.TITLE, facets.mobile.data.model.TaskUpdateField.DESCRIPTION, facets.mobile.data.model.TaskUpdateField.PRIORITY, facets.mobile.data.model.TaskUpdateField.ASSIGNEE))); submitting = false; result.onSuccess { onBack() }.onFailure { error = it.message ?: "Save failed. Your changes are still here; retry." } } }, enabled = !submitting, modifier = Modifier.fillMaxWidth()) { if (submitting) CircularProgressIndicator(Modifier.size(20.dp)) else Text("Save") }
    } }
}

@Composable
fun TaskDetailScreen(task: Task, onBack: () -> Unit, onEdit: () -> Unit, onComment: suspend (String) -> Result<Task>, onClose: suspend (String, List<String>, String?) -> Result<Task>, onReopen: suspend () -> Result<Task>, onDelete: suspend () -> Result<Unit>, modifier: Modifier = Modifier) {
    val scope = rememberCoroutineScope(); var comment by remember { mutableStateOf("") }; var closeMessage by remember { mutableStateOf("") }; var evidence by remember { mutableStateOf("") }; var showClose by remember { mutableStateOf(false) }; var showDelete by remember { mutableStateOf(false) }; var busy by remember { mutableStateOf(false) }; var feedback by remember { mutableStateOf<String?>(null) }
    fun mutation(operation: suspend () -> Result<*>, success: () -> Unit) { if (busy) return; busy = true; scope.launch { operation().onSuccess { success() }.onFailure { feedback = it.message ?: "Operation failed. Try again." }; busy = false } }
    Scaffold(modifier = modifier, topBar = { TopAppBar(title = { Text(task.title) }, navigationIcon = { IconButton(onClick = onBack) { Icon(Icons.Default.ArrowBack, "Back") } }, actions = { TextButton(onClick = onEdit, enabled = !busy) { Text("Edit") } }) }) { padding -> Column(Modifier.padding(padding).padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(task.status.name.lowercase().replaceFirstChar { it.uppercase() }, style = MaterialTheme.typography.labelLarge); Text(task.description.ifBlank { "No description" }); Text("Priority: ${task.priority ?: "None"}"); Text("Assignee: ${task.assignee.ifBlank { "Unassigned" }}"); feedback?.let { Text(it, color = MaterialTheme.colorScheme.primary) }
        if (task.status == TaskStatus.OPEN) { OutlinedTextField(comment, { comment = it }, label = { Text("Comment") }, modifier = Modifier.fillMaxWidth(), minLines = 2); Button(onClick = { if (comment.isBlank()) feedback = "Comment cannot be empty" else mutation({ onComment(comment) }) { comment = ""; feedback = "Comment added" } }, enabled = !busy, modifier = Modifier.fillMaxWidth()) { Text("Add comment") }; OutlinedButton(onClick = { showClose = true }, enabled = !busy, modifier = Modifier.fillMaxWidth()) { Text("Close task") } } else Button(onClick = { mutation({ onReopen() }) { feedback = "Task reopened" } }, enabled = !busy, modifier = Modifier.fillMaxWidth()) { Text("Reopen task") }
        OutlinedButton(onClick = { showDelete = true }, enabled = !busy, modifier = Modifier.fillMaxWidth()) { Text("Delete task") }
    } }
    if (showClose) AlertDialog(onDismissRequest = { if (!busy) showClose = false }, title = { Text("Close task") }, text = { Column(verticalArrangement = Arrangement.spacedBy(8.dp)) { OutlinedTextField(closeMessage, { closeMessage = it }, label = { Text("Message (required)") }, isError = closeMessage.isBlank(), modifier = Modifier.fillMaxWidth()); OutlinedTextField(evidence, { evidence = it }, label = { Text("Evidence (required)") }, isError = evidence.isBlank(), modifier = Modifier.fillMaxWidth(), minLines = 2) } }, confirmButton = { Button(onClick = { if (closeMessage.isBlank() || evidence.isBlank()) return@Button; mutation({ onClose(closeMessage.trim(), evidence.lines().filter(String::isNotBlank), null) }) { showClose = false; feedback = "Task closed" } }, enabled = !busy) { Text("Close") } }, dismissButton = { TextButton(onClick = { showClose = false }, enabled = !busy) { Text("Cancel") } })
    if (showDelete) AlertDialog(onDismissRequest = { if (!busy) showDelete = false }, title = { Text("Delete task?") }, text = { Text("This permanently deletes ${task.title}. This action cannot be undone.") }, confirmButton = { Button(onClick = { mutation({ onDelete() }) { showDelete = false; onBack() } }, enabled = !busy) { Text("Delete") } }, dismissButton = { TextButton(onClick = { showDelete = false }, enabled = !busy) { Text("Cancel") } })
}

@Composable private fun LoadingState() { Column(Modifier.fillMaxSize().padding(24.dp), verticalArrangement = Arrangement.Center) { CircularProgressIndicator(Modifier.semantics { contentDescription = "Loading" }); Spacer(Modifier.height(8.dp)); Text("Loading cached data…") } }
@Composable private fun EmptyState(message: String) { Column(Modifier.fillMaxSize().padding(24.dp), verticalArrangement = Arrangement.Center) { Text(message, style = MaterialTheme.typography.titleMedium) } }
@Composable private fun ErrorState(message: String, retry: () -> Unit) { Column(Modifier.fillMaxSize().padding(24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) { Text(message, color = MaterialTheme.colorScheme.error); Button(onClick = retry) { Text("Retry") } } }
@Composable private fun ErrorBanner(message: String, retry: (() -> Unit)? = null) { Row(Modifier.fillMaxWidth().padding(12.dp), horizontalArrangement = Arrangement.SpaceBetween) { Text(message, color = MaterialTheme.colorScheme.error, modifier = Modifier.weight(1f)); retry?.let { TextButton(onClick = it) { Text("Retry") } } } }
@Composable private fun StaleBanner(message: String, retry: (() -> Unit)? = null) { Row(Modifier.fillMaxWidth().padding(12.dp), horizontalArrangement = Arrangement.SpaceBetween) { Text(message, color = MaterialTheme.colorScheme.tertiary, modifier = Modifier.weight(1f)); retry?.let { TextButton(onClick = it) { Text("Refresh") } } } }

fun encodedProjectRoute(projectId: String) = Uri.encode("projects/$projectId")
fun encodedTaskRoute(projectId: String, taskId: String) = Uri.encode("projects/$projectId/tasks/$taskId")

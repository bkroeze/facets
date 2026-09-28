package facets.mobile.navigation

import android.content.Intent
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.List
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import facets.mobile.R
import facets.mobile.management.ProjectDetailScreen
import facets.mobile.management.ProjectListScreen
import facets.mobile.management.ProjectManagementController
import facets.mobile.management.TaskDetailScreen
import facets.mobile.management.TaskEditorScreen
import facets.mobile.management.TodayScreen
import facets.mobile.management.rememberConfiguredRepository
import kotlinx.coroutines.flow.Flow

private const val PROJECT_ROUTE = "projects/{projectId}"
private const val TASK_ROUTE = "projects/{projectId}/tasks/{taskId}"
private const val EDIT_TASK_ROUTE = "projects/{projectId}/tasks/{taskId}/edit"
private const val CREATE_TASK_ROUTE = "projects/{projectId}/tasks/create"
private const val EXTERNAL_ERROR_ROUTE = "external-error"

@Composable
fun FacetsApp(initialIntent: Intent? = null, intentEvents: Flow<Intent>? = null) {
    val navController = rememberNavController()
    val selectedDestinationState = rememberTopLevelDestination()
    var selectedDestination by selectedDestinationState
    val repository = rememberConfiguredRepository()
    val scope = rememberCoroutineScope()
    val controller = remember(repository, scope) { repository?.let { ProjectManagementController(it, scope) } }
    var externalError by rememberSaveable { mutableStateOf<String?>(null) }

    fun destinationRoute(destination: FacetsDestination): String = when (destination) {
        is FacetsDestination.Project -> FacetsRouteContract.projectNavigationRoute(destination.projectId)
        is FacetsDestination.Task -> FacetsRouteContract.taskNavigationRoute(destination.projectId, destination.taskId)
    }

    fun handleIntent(intent: Intent?) {
        when (val parsed = FacetsRouteContract.parse(intent?.data)) {
            is FacetsRouteParseResult.Valid -> {
                externalError = null
                navController.navigate(destinationRoute(parsed.destination)) { launchSingleTop = true }
            }
            is FacetsRouteParseResult.Invalid -> if (intent?.action == Intent.ACTION_VIEW) {
                externalError = parsed.message
                navController.navigate(EXTERNAL_ERROR_ROUTE) { launchSingleTop = true }
            }
        }
    }

    LaunchedEffect(initialIntent) { handleIntent(initialIntent) }
    LaunchedEffect(intentEvents) { intentEvents?.collect { handleIntent(it) } }

    fun selectTopLevel(destination: TopLevelDestination) {
        selectedDestination = destination
        navController.navigate(destination.route) {
            popUpTo(TopLevelDestination.HOME.route) { saveState = true }
            launchSingleTop = true
            restoreState = true
        }
    }

    Scaffold(
        bottomBar = {
            NavigationBar {
                TopLevelDestination.entries.forEach { destination ->
                    NavigationBarItem(
                        selected = selectedDestination == destination,
                        onClick = { selectTopLevel(destination) },
                        icon = { Icon(destination.icon, contentDescription = destination.label) },
                        label = { Text(destination.label) },
                    )
                }
            }
        },
    ) { innerPadding ->
        NavHost(
            navController = navController,
            startDestination = TopLevelDestination.HOME.route,
            modifier = Modifier.padding(innerPadding),
        ) {
            composable(TopLevelDestination.HOME.route) {
                if (controller == null) {
                    ProjectManagementRootFallback("Connect a server in Settings to see today's focus and top tasks.")
                } else {
                    TodayScreen(
                        controller = controller,
                        onOpenTask = { projectId, taskId ->
                            navController.navigate(FacetsRouteContract.taskNavigationRoute(projectId, taskId))
                        },
                    )
                }
            }
            composable(TopLevelDestination.TASKS.route) {
                DestinationScreen(TopLevelDestination.TASKS, R.string.tasks_description)
            }
            composable(TopLevelDestination.PROJECTS.route) {
                if (controller == null) {
                    ProjectManagementRootFallback()
                } else {
                    ProjectListScreen(
                        controller = controller,
                        onOpenProject = { projectId -> navController.navigate(FacetsRouteContract.projectNavigationRoute(projectId)) },
                    )
                }
            }
            composable(PROJECT_ROUTE) { entry ->
                val projectId = entry.arguments?.getString("projectId")
                val projectState = controller?.projects?.collectAsState()?.value
                when {
                    controller == null || projectId == null -> InvalidExternalTargetScreen(
                        "This project link is invalid.",
                    ) { navController.popBackStack() }
                    projectState?.loading == false && projectState.projects.none { it.id == projectId } ->
                        InvalidExternalTargetScreen("That project is unavailable or has been deleted.") {
                            navController.popBackStack()
                        }
                    else -> ProjectDetailScreen(
                        controller = controller,
                        projectId = projectId,
                        onBack = { navController.popBackStack() },
                        onOpenTask = { id, taskId ->
                            navController.navigate(FacetsRouteContract.taskNavigationRoute(id, taskId))
                        },
                        onCreateTask = { id ->
                            navController.navigate("projects/$id/tasks/create")
                        },
                    )
                }
            }
            composable(CREATE_TASK_ROUTE) { entry ->
                val projectId = entry.arguments?.getString("projectId")
                if (controller != null && projectId != null) {
                    TaskEditorScreen(
                        initial = null,
                        onBack = { navController.popBackStack() },
                        onSave = { create, _ -> controller.createTask(projectId, create!!) },
                    )
                } else {
                    InvalidExternalTargetScreen("This project link is invalid.") { navController.popBackStack() }
                }
            }
            composable(TASK_ROUTE) { entry ->
                val projectId = entry.arguments?.getString("projectId")
                val taskId = entry.arguments?.getString("taskId")
                LaunchedEffect(projectId) {
                    if (controller != null && projectId != null) controller.selectProject(projectId)
                }
                val detailState = controller?.detail?.collectAsState()?.value
                val task = detailState?.tasks?.firstOrNull { it.id == taskId }
                when {
                    controller == null || projectId == null || taskId == null ->
                        InvalidExternalTargetScreen("This project or task link is invalid.") {
                            navController.popBackStack()
                        }
                    detailState?.loading == false && task == null ->
                        InvalidExternalTargetScreen("That task is unavailable or has been deleted.") {
                            navController.popBackStack()
                        }
                    task != null -> {
                        val project = projectId
                        val taskKey = taskId
                        TaskDetailScreen(
                            task = task,
                            onBack = { navController.popBackStack() },
                            onEdit = { navController.navigate("projects/$project/tasks/$taskKey/edit") },
                            onComment = { body -> controller.commentTask(project, taskKey, body) },
                            onClose = { message, evidence, comment ->
                                controller.closeTask(project, taskKey, message, evidence, comment)
                            },
                            onReopen = { controller.reopenTask(project, taskKey) },
                            onDelete = { controller.deleteTask(project, taskKey) },
                        )
                    }
                    else -> ProjectManagementRootFallback("Loading project and task…")
                }
            }
            composable(EDIT_TASK_ROUTE) { entry ->
                val projectId = entry.arguments?.getString("projectId")
                val taskId = entry.arguments?.getString("taskId")
                val task = controller?.detail?.collectAsState()?.value?.tasks?.firstOrNull { it.id == taskId }
                if (controller != null && projectId != null && taskId != null && task != null) {
                    val project = projectId
                    val taskKey = taskId
                    TaskEditorScreen(
                        initial = task,
                        onBack = { navController.popBackStack() },
                        onSave = { _, update -> controller.updateTask(project, taskKey, update!!) },
                    )
                } else {
                    InvalidExternalTargetScreen("That task is unavailable or has been deleted.") {
                        navController.popBackStack()
                    }
                }
            }
            composable(EXTERNAL_ERROR_ROUTE) {
                InvalidExternalTargetScreen(externalError ?: "This link could not be opened.") {
                    externalError = null
                    navController.popBackStack()
                }
            }
            composable(TopLevelDestination.SETTINGS.route) {
                facets.mobile.settings.ServerSettingsScreen()
            }
        }
    }
}

@Composable
private fun ProjectManagementRootFallback(message: String = "Connect a server in Settings to browse projects.") {
    Column(
        Modifier.fillMaxSize(),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(message)
    }
}

@Composable
private fun InvalidExternalTargetScreen(message: String, onBack: () -> Unit) {
    Column(
        Modifier.fillMaxSize().padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text("Unable to open link", style = MaterialTheme.typography.headlineSmall)
        Text(message)
        Button(onClick = onBack) { Text("Back") }
    }
}

private val TopLevelDestination.icon
    get() = when (this) {
        TopLevelDestination.HOME -> Icons.Default.Home
        TopLevelDestination.TASKS -> Icons.Default.List
        TopLevelDestination.PROJECTS -> Icons.Default.Settings
        TopLevelDestination.SETTINGS -> Icons.Default.Settings
    }

@Composable
private fun DestinationScreen(destination: TopLevelDestination, descriptionRes: Int) {
    Column(
        modifier = Modifier.fillMaxSize(),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(destination.label, style = MaterialTheme.typography.headlineMedium)
        Text(stringResource(descriptionRes), style = MaterialTheme.typography.bodyLarge)
    }
}

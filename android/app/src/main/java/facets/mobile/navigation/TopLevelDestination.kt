package facets.mobile.navigation

import androidx.compose.runtime.Composable
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.Saver
import androidx.compose.runtime.saveable.rememberSaveable

/** The destinations available from the app's persistent navigation bar. */
enum class TopLevelDestination(
    val route: String,
    val label: String,
) {
    HOME("home", "Today"),
    TASKS("tasks", "Tasks"),
    PROJECTS("projects", "Projects"),
    SETTINGS("settings", "Settings"),
}

private val topLevelDestinationSaver = Saver<MutableState<TopLevelDestination>, String>(
    save = { it.value.name },
    restore = { savedName -> mutableStateOf(restoreTopLevelDestination(savedName)) },
)

/** Restores a destination saved by Compose, falling back safely for old state. */
fun restoreTopLevelDestination(savedName: String?): TopLevelDestination =
    savedName?.let { name ->
        TopLevelDestination.entries.firstOrNull { it.name == name }
    } ?: TopLevelDestination.HOME

@Composable
fun rememberTopLevelDestination(): MutableState<TopLevelDestination> = rememberSaveable(
    saver = topLevelDestinationSaver,
) {
    mutableStateOf(TopLevelDestination.HOME)
}

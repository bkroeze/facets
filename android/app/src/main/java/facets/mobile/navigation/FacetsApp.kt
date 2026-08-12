package facets.mobile.navigation

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.List
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.NavGraph.Companion.findStartDestination
import facets.mobile.R

@Composable
fun FacetsApp() {
    val navController = rememberNavController()
    val selectedDestinationState = rememberTopLevelDestination()
    var selectedDestination by selectedDestinationState

    LaunchedEffect(selectedDestination) {
        if (navController.currentDestination?.route != selectedDestination.route) {
            navController.navigate(selectedDestination.route) {
                popUpTo(navController.graph.findStartDestination().id) {
                    saveState = true
                }
                launchSingleTop = true
                restoreState = true
            }
        }
    }

    Scaffold(
        bottomBar = {
            NavigationBar {
                TopLevelDestination.entries.forEach { destination ->
                    NavigationBarItem(
                        selected = selectedDestination == destination,
                        onClick = { selectedDestination = destination },
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
                DestinationScreen(TopLevelDestination.HOME, R.string.home_description)
            }
            composable(TopLevelDestination.TASKS.route) {
                DestinationScreen(TopLevelDestination.TASKS, R.string.tasks_description)
            }
            composable(TopLevelDestination.PROJECTS.route) {
                DestinationScreen(TopLevelDestination.PROJECTS, R.string.projects_description)
            }
        }
    }
}

private val TopLevelDestination.icon
    get() = when (this) {
        TopLevelDestination.HOME -> Icons.Default.Home
        TopLevelDestination.TASKS -> Icons.Default.List
        TopLevelDestination.PROJECTS -> Icons.Default.Settings
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

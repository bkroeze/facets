@file:OptIn(ExperimentalMaterial3Api::class)
package facets.mobile.management

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.launch

@Composable
fun TodayScreen(
    controller: ProjectManagementController,
    onOpenTask: (String, String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val state by controller.today.collectAsState()
    val scope = rememberCoroutineScope()
    var focusText by remember(state.snapshot?.focus?.text) { mutableStateOf(state.snapshot?.focus?.text.orEmpty()) }
    var focusError by remember { mutableStateOf<String?>(null) }
    var savingFocus by remember { mutableStateOf(false) }


    Scaffold(
        modifier = modifier,
        topBar = {
            TopAppBar(
                title = { Text("Today") },
                actions = {
                    IconButton(
                        onClick = controller::refreshToday,
                        enabled = !state.refreshing,
                        modifier = Modifier.semantics { contentDescription = "Refresh today" },
                    ) { Icon(Icons.Default.Refresh, contentDescription = null) }
                },
            )
        },
    ) { padding ->
        Column(Modifier.padding(padding).fillMaxSize()) {
            if (state.refreshing) LinearProgressIndicator(Modifier.fillMaxWidth())
            state.error?.let { error ->
                Column(Modifier.fillMaxWidth().padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text(error, color = MaterialTheme.colorScheme.error)
                    Button(onClick = controller::refreshToday) { Text("Retry") }
                }
            }
            if (state.loading && state.snapshot == null) {
                Column(Modifier.fillMaxSize().padding(24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    CircularProgressIndicator()
                    Text("Loading today's focus…")
                }
            } else {
                val snapshot = state.snapshot
                LazyColumn(
                    modifier = Modifier.fillMaxSize(),
                    contentPadding = PaddingValues(16.dp),
                    verticalArrangement = Arrangement.spacedBy(12.dp),
                ) {
                    item {
                        Card(Modifier.fillMaxWidth()) {
                            Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                                Text("Today's focus", style = MaterialTheme.typography.titleMedium)
                                OutlinedTextField(
                                    value = focusText,
                                    onValueChange = { focusText = it; focusError = null },
                                    label = { Text("What matters most?") },
                                    supportingText = { focusError?.let { Text(it) } },
                                    isError = focusError != null,
                                    modifier = Modifier.fillMaxWidth(),
                                    minLines = 2,
                                )
                                Button(
                                    onClick = {
                                        if (focusText.isBlank()) {
                                            focusError = "Focus cannot be empty"
                                        } else if (!savingFocus) {
                                            savingFocus = true
                                            scope.launch {
                                                controller.setTodayFocus(focusText).onFailure {
                                                    focusError = it.message ?: "Unable to save today's focus"
                                                }
                                                savingFocus = false
                                            }
                                        }
                                    },
                                    enabled = !savingFocus,
                                ) { Text(if (savingFocus) "Saving…" else "Save focus") }
                            }
                        }
                    }
                    item {
                        val completion = snapshot?.completedToday
                        Card(Modifier.fillMaxWidth()) {
                            Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                                Text("Completed today", style = MaterialTheme.typography.titleMedium)
                                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                                    Text("All tasks")
                                    Text((completion?.all ?: 0).toString(), style = MaterialTheme.typography.titleLarge)
                                }
                                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                                    Text("Top tasks")
                                    Text((completion?.top ?: 0).toString(), style = MaterialTheme.typography.titleLarge)
                                }
                            }
                        }
                    }
                    item {
                        Text("Top tasks", style = MaterialTheme.typography.titleMedium)
                    }
                    if (snapshot?.topTasks.isNullOrEmpty()) {
                        item { Text("No top tasks for today.") }
                    } else {
                        items(snapshot!!.topTasks, key = { "${it.projectId}:${it.taskId}" }) { task ->
                            Card(
                                onClick = { onOpenTask(task.projectId, task.taskId) },
                                modifier = Modifier.fillMaxWidth().semantics {
                                    contentDescription = "Open top task ${task.title} in ${task.projectName}"
                                },
                            ) {
                                Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                                    Text(task.title, style = MaterialTheme.typography.titleMedium)
                                    Text("${task.projectName} · ${task.taskId}", style = MaterialTheme.typography.labelMedium)
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

package facets.mobile.widget

import android.content.Context
import android.content.Intent
import androidx.compose.runtime.Composable
import androidx.compose.ui.unit.dp
import androidx.glance.GlanceId
import androidx.glance.GlanceModifier
import androidx.glance.action.ActionParameters
import androidx.glance.action.actionParametersOf
import androidx.glance.action.clickable
import androidx.glance.appwidget.GlanceAppWidget
import androidx.glance.appwidget.GlanceAppWidgetReceiver
import androidx.glance.appwidget.action.ActionCallback
import androidx.glance.appwidget.action.actionRunCallback
import androidx.glance.appwidget.provideContent
import androidx.glance.appwidget.updateAll
import androidx.glance.layout.Column
import androidx.glance.layout.Row
import androidx.glance.layout.Spacer
import androidx.glance.layout.fillMaxSize
import androidx.glance.layout.fillMaxWidth
import androidx.glance.layout.height
import androidx.glance.layout.padding
import androidx.glance.layout.width
import androidx.glance.text.Text
import facets.mobile.FacetsApplication
import facets.mobile.MainActivity
import facets.mobile.data.cache.ProjectEntity
import facets.mobile.navigation.FacetsRouteContract
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.withContext

private const val PREFS = "project_widget"
private const val SELECTED_PREFIX = "selected_"
private const val MAX_PROJECTS = 8

private sealed interface Snapshot {
    data class Ready(val projects: List<ProjectEntity>) : Snapshot
    data class Error(val message: String) : Snapshot
}

class ProjectWidget : GlanceAppWidget() {
    override suspend fun provideGlance(context: Context, id: GlanceId) {
        val snapshot = runCatching {
            val app = context.applicationContext as FacetsApplication
            withContext(Dispatchers.IO) { app.database.projects().observeAll().first() }
        }.fold(
            onSuccess = { Snapshot.Ready(it) },
            onFailure = { Snapshot.Error("Cached project data is unavailable") },
        )
        val selected = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            .getStringSet("$SELECTED_PREFIX$id", emptySet())
            .orEmpty()
        provideContent { ProjectWidgetContent(context, snapshot, selected) }
    }
}

class ProjectWidgetReceiver : GlanceAppWidgetReceiver() {
    override val glanceAppWidget: GlanceAppWidget = ProjectWidget()

    override fun onDeleted(context: Context, appWidgetIds: IntArray) {
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().apply {
            appWidgetIds.forEach { remove("$SELECTED_PREFIX$it") }
            apply()
        }
        super.onDeleted(context, appWidgetIds)
    }
}

class OpenProjectAction : ActionCallback {
    override suspend fun onAction(context: Context, glanceId: GlanceId, parameters: ActionParameters) {
        val projectId = parameters[PROJECT_ID] ?: return
        context.startActivity(
            Intent(Intent.ACTION_VIEW, FacetsRouteContract.projectUri(projectId))
                .setClass(context, MainActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
        )
    }

    companion object {
        val PROJECT_ID = ActionParameters.Key<String>("project_id")
    }
}

class RefreshProjectWidgetAction : ActionCallback {
    override suspend fun onAction(context: Context, glanceId: GlanceId, parameters: ActionParameters) {
        val app = context.applicationContext as FacetsApplication
        app.syncScheduler.enqueueWidgetRefresh()
        ProjectWidget().updateAll(context)
    }
}

@Composable
private fun ProjectWidgetContent(context: Context, snapshot: Snapshot, selectedIds: Set<String>) {
    val projects = when (snapshot) {
        is Snapshot.Ready -> if (selectedIds.isEmpty()) snapshot.projects else snapshot.projects.filter { it.id in selectedIds }
        is Snapshot.Error -> emptyList()
    }.take(MAX_PROJECTS)
    Column(GlanceModifier.fillMaxSize().padding(12.dp)) {
        Row(GlanceModifier.fillMaxWidth()) {
            Text("Facets")
            Spacer(GlanceModifier.width(8.dp))
            Text("Refresh", modifier = GlanceModifier.clickable(actionRunCallback<RefreshProjectWidgetAction>()))
        }
        Spacer(GlanceModifier.height(4.dp))
        when {
            snapshot is Snapshot.Error -> Text(snapshot.message)
            projects.isEmpty() -> Text(if (selectedIds.isEmpty()) "No projects yet" else "No selected projects")
            else -> projects.forEach { project ->
                Row(
                    GlanceModifier.fillMaxWidth().padding(vertical = 4.dp).clickable(
                        actionRunCallback<OpenProjectAction>(
                            actionParametersOf(OpenProjectAction.PROJECT_ID.to(project.id)),
                        ),
                    ),
                ) {
                    Text(project.name, maxLines = 1)
                    Spacer(GlanceModifier.width(8.dp))
                    Text(project.activeTaskCount.toString())
                }
            }
        }
    }
}

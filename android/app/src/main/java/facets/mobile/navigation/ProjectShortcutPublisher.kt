package facets.mobile.navigation

import android.content.Context
import android.content.Intent
import android.content.pm.ShortcutInfo
import android.content.pm.ShortcutManager
import android.graphics.drawable.Icon
import facets.mobile.data.cache.ProjectEntity

/** Publishes only current, valid project targets; stale dynamic entries are removed. */
class ProjectShortcutPublisher(context: Context) {
    private val appContext = context.applicationContext
    private val shortcutManager: ShortcutManager? =
        appContext.getSystemService(ShortcutManager::class.java)

    fun publish(projects: List<ProjectEntity>) {
        val manager = shortcutManager ?: return
        val limit = manager.maxShortcutCountPerActivity.coerceAtMost(MAX_PROJECT_SHORTCUTS)
        if (limit <= 0) return
        val current = projects
            .asSequence()
            .filter { FacetsRouteContract.isSafeOpaqueId(it.id) }
            .distinctBy(ProjectEntity::id)
            .sortedWith(
                compareByDescending<ProjectEntity> { it.updatedAtEpochMillis ?: it.createdAtEpochMillis }
                    .thenBy(String.CASE_INSENSITIVE_ORDER) { it.name }
                    .thenBy { it.id },
            )
            .take(limit)
            .toList()
        val desiredIds = current.map { shortcutId(it.id) }.toSet()
        val staleIds = manager.dynamicShortcuts.map(ShortcutInfo::getId).filterNot(desiredIds::contains)
        runCatching {
            if (staleIds.isNotEmpty()) manager.removeDynamicShortcuts(staleIds)
            manager.setDynamicShortcuts(current.mapIndexed { rank, project -> project.toShortcut(rank) })
        }
    }

    private fun ProjectEntity.toShortcut(rank: Int): ShortcutInfo = ShortcutInfo.Builder(appContext, shortcutId(id))
        .setShortLabel(name.ifBlank { "Project ${id}" }.take(MAX_SHORT_LABEL_LENGTH))
        .setLongLabel("Open ${name.ifBlank { "Project ${id}" }}".take(MAX_LONG_LABEL_LENGTH))
        .setIcon(Icon.createWithResource(appContext, android.R.drawable.ic_menu_view))
        .setRank(rank)
        .setIntent(
            Intent(Intent.ACTION_VIEW, FacetsRouteContract.projectUri(id)).setPackage(appContext.packageName),
        )
        .build()

    private fun shortcutId(projectId: String): String = "project:$projectId"

    private companion object {
        const val MAX_PROJECT_SHORTCUTS = 4
        const val MAX_SHORT_LABEL_LENGTH = 40
        const val MAX_LONG_LABEL_LENGTH = 80
    }
}

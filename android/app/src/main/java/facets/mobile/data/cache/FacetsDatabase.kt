package facets.mobile.data.cache

import android.content.Context
import androidx.room.Database
import androidx.room.Room
import androidx.room.RoomDatabase
import androidx.room.migration.Migration
import androidx.sqlite.db.SupportSQLiteDatabase
import androidx.room.withTransaction

@Database(
    entities = [ProjectEntity::class, TaskEntity::class, SavedViewEntity::class, SyncStateEntity::class],
    version = 3,
)
abstract class FacetsDatabase : RoomDatabase() {
    abstract fun projects(): ProjectDao
    abstract fun tasks(): TaskDao
    abstract fun views(): SavedViewDao
    abstract fun syncState(): SyncStateDao

    suspend fun replaceProjectSnapshot(projects: List<ProjectEntity>, configuredProjectIds: Set<String>? = null) {
        withTransaction {
            val retained = configuredProjectIds?.let { ids -> projects.filter { it.id in ids } } ?: projects
            if (retained.isEmpty()) this@FacetsDatabase.projects().deleteAll()
            else this@FacetsDatabase.projects().deleteAbsent(retained.map { it.id })
            this@FacetsDatabase.projects().upsertAll(retained)
        }
    }

    suspend fun replaceTaskSnapshot(projectId: String, tasks: List<TaskEntity>) {
        withTransaction {
            this@FacetsDatabase.tasks().deleteForProject(projectId)
            this@FacetsDatabase.tasks().upsertAll(tasks.filter { it.projectId == projectId })
        }
    }

    suspend fun replaceViewSnapshot(views: List<SavedViewEntity>, configuredViewIds: Set<String>? = null) {
        withTransaction {
            val retained = configuredViewIds?.let { ids -> views.filter { it.id in ids } } ?: views
            if (retained.isEmpty()) this@FacetsDatabase.views().deleteAll()
            else this@FacetsDatabase.views().deleteAbsent(retained.map { it.id })
            this@FacetsDatabase.views().upsertAll(retained)
        }
    }

    companion object {
        const val PROJECTS_SCOPE = "projects"
        const val VIEWS_SCOPE = "views"
        fun projectTasksScope(projectId: String) = "tasks:$projectId"
        const val SYNC_SCOPE = "sync"

        val MIGRATION_1_2: Migration = object : Migration(1, 2) {
            override fun migrate(db: SupportSQLiteDatabase) {
                db.execSQL(
                    "CREATE TABLE IF NOT EXISTS sync_state (scope TEXT NOT NULL, lastSuccessEpochMillis INTEGER, lastError TEXT, PRIMARY KEY(scope))",
                )
            }
        }

        val MIGRATION_2_3: Migration = object : Migration(2, 3) {
            override fun migrate(db: SupportSQLiteDatabase) {
                db.execSQL("ALTER TABLE sync_state ADD COLUMN lastAttemptEpochMillis INTEGER")
            }
        }

        fun create(context: Context): FacetsDatabase = Room.databaseBuilder(
            context.applicationContext,
            FacetsDatabase::class.java,
            "facets-cache.db",
        ).addMigrations(MIGRATION_1_2, MIGRATION_2_3).build()
    }
}

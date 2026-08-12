package facets.mobile.data.cache

import androidx.room.Dao
import androidx.room.Entity
import androidx.room.ForeignKey
import androidx.room.Index
import androidx.room.Query
import androidx.room.Transaction
import androidx.room.Upsert
import kotlinx.coroutines.flow.Flow

@Entity(tableName = "projects")
data class ProjectEntity(
    @androidx.room.PrimaryKey val id: String,
    val name: String,
    val description: String,
    val activeTaskCount: Int,
    val createdAtEpochMillis: Long,
    val updatedAtEpochMillis: Long?,
)

@Entity(
    tableName = "tasks",
    foreignKeys = [ForeignKey(
        entity = ProjectEntity::class,
        parentColumns = ["id"],
        childColumns = ["projectId"],
        onDelete = ForeignKey.CASCADE,
    )],
    indices = [Index("projectId"), Index("updatedAtEpochMillis")],
)
data class TaskEntity(
    @androidx.room.PrimaryKey val id: String,
    val projectId: String,
    val title: String,
    val description: String,
    val status: String,
    val priority: Int?,
    val assignee: String,
    val createdAtEpochMillis: Long,
    val updatedAtEpochMillis: Long,
)

@Entity(tableName = "saved_views")
data class SavedViewEntity(
    @androidx.room.PrimaryKey val id: String,
    val name: String,
    val builtin: Boolean,
    val statusesJson: String,
    val assigneesJson: String,
    val prioritiesJson: String,
    val orderField: String,
    val orderDirection: String,
    val createdAtEpochMillis: Long?,
    val updatedAtEpochMillis: Long?,
)

@Entity(tableName = "sync_state")
data class SyncStateEntity(
    @androidx.room.PrimaryKey val scope: String,
    val lastAttemptEpochMillis: Long?,
    val lastSuccessEpochMillis: Long?,
    val lastError: String?,
)

data class ProjectWithTasks(
    @androidx.room.Embedded val project: ProjectEntity,
    @androidx.room.Relation(parentColumn = "id", entityColumn = "projectId")
    val tasks: List<TaskEntity>,
)

@Dao
interface ProjectDao {
    @Query("SELECT * FROM projects ORDER BY name COLLATE NOCASE, id")
    fun observeAll(): Flow<List<ProjectEntity>>

    @Query("SELECT * FROM projects WHERE id = :projectId")
    suspend fun get(projectId: String): ProjectEntity?
    @Transaction
    @Query("SELECT * FROM projects WHERE id = :projectId")
    suspend fun getWithTasks(projectId: String): ProjectWithTasks?

    @Query("SELECT * FROM projects WHERE id IN (:projectIds) ORDER BY name COLLATE NOCASE, id")
    suspend fun getConfigured(projectIds: Set<String>): List<ProjectEntity>

    @Query("SELECT * FROM projects ORDER BY name COLLATE NOCASE, id")
    suspend fun getAll(): List<ProjectEntity>

    @Upsert
    suspend fun upsertAll(projects: List<ProjectEntity>)

    @Query("DELETE FROM projects")
    suspend fun deleteAll()

    @Query("DELETE FROM projects WHERE id NOT IN (:retainedIds)")
    suspend fun deleteAbsent(retainedIds: List<String>)
}

@Dao
interface TaskDao {
    @Query("SELECT * FROM tasks WHERE projectId = :projectId ORDER BY updatedAtEpochMillis DESC, id")
    fun observeForProject(projectId: String): Flow<List<TaskEntity>>

    @Query("SELECT * FROM tasks WHERE projectId = :projectId ORDER BY updatedAtEpochMillis DESC, id")
    suspend fun getForProject(projectId: String): List<TaskEntity>

    @Query("SELECT * FROM tasks WHERE id = :taskId")
    suspend fun get(taskId: String): TaskEntity?

    @Upsert
    suspend fun upsert(task: TaskEntity)

    @Upsert
    suspend fun upsertAll(tasks: List<TaskEntity>)

    @Query("DELETE FROM tasks WHERE projectId = :projectId")
    suspend fun deleteForProject(projectId: String)

    @Query("DELETE FROM tasks WHERE id = :taskId")
    suspend fun delete(taskId: String)
}

@Dao
interface SavedViewDao {
    @Query("SELECT * FROM saved_views ORDER BY builtin DESC, name COLLATE NOCASE, id")
    fun observeAll(): Flow<List<SavedViewEntity>>

    @Query("SELECT * FROM saved_views ORDER BY builtin DESC, name COLLATE NOCASE, id")
    suspend fun getAll(): List<SavedViewEntity>

    @Query("SELECT * FROM saved_views WHERE id = :viewId")
    suspend fun get(viewId: String): SavedViewEntity?

    @Upsert
    suspend fun upsertAll(views: List<SavedViewEntity>)

    @Upsert
    suspend fun upsert(view: SavedViewEntity)

    @Query("DELETE FROM saved_views")
    suspend fun deleteAll()

    @Query("DELETE FROM saved_views WHERE id = :viewId")
    suspend fun delete(viewId: String)

    @Query("DELETE FROM saved_views WHERE id NOT IN (:retainedIds)")
    suspend fun deleteAbsent(retainedIds: List<String>)
}

@Dao
interface SyncStateDao {
    @Query("SELECT * FROM sync_state WHERE scope = :scope")
    suspend fun get(scope: String): SyncStateEntity?
    @Query("SELECT * FROM sync_state WHERE scope = :scope")
    fun observe(scope: String): Flow<SyncStateEntity?>

    @Upsert
    suspend fun upsert(state: SyncStateEntity)
}

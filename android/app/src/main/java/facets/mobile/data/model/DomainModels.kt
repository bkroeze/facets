package facets.mobile.data.model

import java.time.Instant

/** Stable, UI-independent representations of API v1 resources. */
data class Project(
    val id: String,
    val name: String,
    val description: String,
    val activeTaskCount: Int,
    val createdAt: Instant,
    val updatedAt: Instant?,
)

enum class TaskStatus { OPEN, CLOSED }

enum class TaskListStatus { OPEN, CLOSED, ALL }

data class Task(
    val id: String,
    val projectId: String,
    val title: String,
    val description: String,
    val status: TaskStatus,
    val priority: Int?,
    val assignee: String,
    val createdAt: Instant,
    val updatedAt: Instant,
)

data class ViewQuery(
    val statuses: List<TaskStatus> = emptyList(),
    val assignees: List<String> = emptyList(),
    val priorities: List<Int> = emptyList(),
)

enum class ViewOrderField { TITLE, STATUS, PRIORITY, ASSIGNEE, CREATED_AT, UPDATED_AT }
enum class ViewOrderDirection { ASC, DESC }

data class ViewOrder(
    val field: ViewOrderField,
    val direction: ViewOrderDirection,
)

data class SavedView(
    val id: String,
    val name: String,
    val builtin: Boolean,
    val query: ViewQuery,
    val order: ViewOrder,
    val createdAt: Instant?,
    val updatedAt: Instant?,
)

data class CreateTaskRequest(
    val title: String,
    val description: String? = null,
    val priority: Int? = null,
    val assignee: String? = null,
    val idempotencyKey: String? = null,
)

enum class TaskUpdateField { TITLE, DESCRIPTION, PRIORITY, ASSIGNEE }

data class UpdateTaskRequest(
    val title: String? = null,
    val description: String? = null,
    val priority: Int? = null,
    val assignee: String? = null,
    /** Include a field here when its value is intentionally null (for example, to clear priority). */
    val fields: Set<TaskUpdateField> = emptySet(),
) {
    internal fun presentFields(): Set<String> = buildSet {
        if (title != null || TaskUpdateField.TITLE in fields) add("title")
        if (description != null || TaskUpdateField.DESCRIPTION in fields) add("description")
        if (priority != null || TaskUpdateField.PRIORITY in fields) add("priority")
        if (assignee != null || TaskUpdateField.ASSIGNEE in fields) add("assignee")
    }
}

data class CommentRequest(val body: String)

data class CloseTaskRequest(
    val message: String,
    val evidence: List<String>,
    val comment: String? = null,
)

data class CreateViewRequest(
    val name: String,
    val query: ViewQuery = ViewQuery(),
    val order: ViewOrder? = null,
)

enum class ViewUpdateField { NAME, QUERY, ORDER }

data class UpdateViewRequest(
    val name: String? = null,
    val query: ViewQuery? = null,
    val order: ViewOrder? = null,
    val fields: Set<ViewUpdateField> = emptySet(),
) {
    internal fun presentFields(): Set<String> = buildSet {
        if (name != null || ViewUpdateField.NAME in fields) add("name")
        if (query != null || ViewUpdateField.QUERY in fields) add("query")
        if (order != null || ViewUpdateField.ORDER in fields) add("order")
    }
}

data class HealthStatus(val healthy: Boolean)

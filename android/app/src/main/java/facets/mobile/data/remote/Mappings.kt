package facets.mobile.data.remote

import facets.mobile.data.model.CloseTaskRequest
import facets.mobile.data.model.CommentRequest
import facets.mobile.data.model.CreateTaskRequest
import facets.mobile.data.model.CreateViewRequest
import facets.mobile.data.model.Project
import facets.mobile.data.model.SavedView
import facets.mobile.data.model.Task
import facets.mobile.data.model.TaskStatus
import facets.mobile.data.model.TaskUpdateField
import facets.mobile.data.model.TodayCompletion
import facets.mobile.data.model.TodayFocus
import facets.mobile.data.model.TodaySnapshot
import facets.mobile.data.model.TodayTask
import facets.mobile.data.model.UpdateTaskRequest
import facets.mobile.data.model.UpdateViewRequest
import facets.mobile.data.model.ViewOrder
import facets.mobile.data.model.ViewOrderDirection
import facets.mobile.data.model.ViewOrderField
import facets.mobile.data.model.ViewQuery
import facets.mobile.data.transport.MalformedResponseException
import java.time.Instant
import java.time.OffsetDateTime
import java.time.ZoneOffset
import java.time.format.DateTimeFormatter
import java.time.format.DateTimeParseException

internal fun String.toApiInstant(field: String): Instant = try {
    val parsed = OffsetDateTime.parse(this, DateTimeFormatter.ISO_OFFSET_DATE_TIME)
    if (parsed.offset.totalSeconds != 0) {
        throw MalformedResponseException("$field must be a UTC timestamp")
    }
    parsed.toInstant()
} catch (error: DateTimeParseException) {
    throw MalformedResponseException("Invalid RFC 3339 timestamp in $field", error)
}

internal fun String?.toOptionalApiInstant(field: String): Instant? = this?.toApiInstant(field)

internal fun ProjectDto.toDomain(): Project = Project(
    id = id,
    name = name,
    description = description,
    activeTaskCount = activeTaskCount,
    createdAt = createdAt.toApiInstant("created_at"),
    updatedAt = updatedAt.toOptionalApiInstant("updated_at"),
)

internal fun TaskDto.toDomain(): Task {
    val validatedPriority = priority?.also {
        if (it !in 0..4) throw MalformedResponseException("Task priority must be between 0 and 4")
    }
    return Task(
        id = id,
        projectId = projectId,
        title = title,
        description = description,
        status = status.toDomain(),
        priority = validatedPriority,
        assignee = assignee,
        createdAt = createdAt.toApiInstant("created_at"),
        updatedAt = updatedAt.toApiInstant("updated_at"),
        top = top,
    )
}
internal fun TaskStatusDto.toDomain(): TaskStatus = when (this) {
    TaskStatusDto.OPEN -> TaskStatus.OPEN
    TaskStatusDto.CLOSED -> TaskStatus.CLOSED
}

internal fun ViewQueryDto.toDomain(): ViewQuery = ViewQuery(
    statuses = statuses.map { it.toDomain() },
    assignees = assignees,
    priorities = priorities.map {
        if (it !in 0..4) throw MalformedResponseException("Saved-view priority must be between 0 and 4")
        it
    },
)

internal fun ViewOrderDto.toDomain(): ViewOrder = ViewOrder(
    field = when (field) {
        ViewOrderFieldDto.TITLE -> ViewOrderField.TITLE
        ViewOrderFieldDto.STATUS -> ViewOrderField.STATUS
        ViewOrderFieldDto.PRIORITY -> ViewOrderField.PRIORITY
        ViewOrderFieldDto.ASSIGNEE -> ViewOrderField.ASSIGNEE
        ViewOrderFieldDto.CREATED_AT -> ViewOrderField.CREATED_AT
        ViewOrderFieldDto.UPDATED_AT -> ViewOrderField.UPDATED_AT
    },
    direction = when (direction) {
        ViewOrderDirectionDto.ASC -> ViewOrderDirection.ASC
        ViewOrderDirectionDto.DESC -> ViewOrderDirection.DESC
    },
)

internal fun SavedViewDto.toDomain(): SavedView = SavedView(
    id = id,
    name = name,
    builtin = builtin,
    query = query.toDomain(),
    order = order.toDomain(),
    createdAt = createdAt.toOptionalApiInstant("created_at"),
    updatedAt = updatedAt.toOptionalApiInstant("updated_at"),
)

internal fun TaskStatus.toWire(): TaskStatusDto = when (this) {
    TaskStatus.OPEN -> TaskStatusDto.OPEN
    TaskStatus.CLOSED -> TaskStatusDto.CLOSED
}

internal fun ViewQuery.toWire(): ViewQueryDto = ViewQueryDto(
    statuses = statuses.map { it.toWire() },
    assignees = assignees,
    priorities = priorities,
)

internal fun ViewOrder.toWire(): ViewOrderDto = ViewOrderDto(
    field = when (field) {
        ViewOrderField.TITLE -> ViewOrderFieldDto.TITLE
        ViewOrderField.STATUS -> ViewOrderFieldDto.STATUS
        ViewOrderField.PRIORITY -> ViewOrderFieldDto.PRIORITY
        ViewOrderField.ASSIGNEE -> ViewOrderFieldDto.ASSIGNEE
        ViewOrderField.CREATED_AT -> ViewOrderFieldDto.CREATED_AT
        ViewOrderField.UPDATED_AT -> ViewOrderFieldDto.UPDATED_AT
    },
    direction = when (direction) {
        ViewOrderDirection.ASC -> ViewOrderDirectionDto.ASC
        ViewOrderDirection.DESC -> ViewOrderDirectionDto.DESC
    },
)

internal fun CreateTaskRequest.toWire(): CreateTaskDto = CreateTaskDto(
    title = title,
    description = description,
    priority = priority,
    assignee = assignee,
    idempotencyKey = idempotencyKey,
)

internal fun UpdateTaskRequest.toWire(): TaskPatchDto = TaskPatchDto(
    title = title,
    description = description,
    priority = priority,
    assignee = assignee,
    top = top,
    presentFields = presentFields(),
)

internal fun TodayFocusDto.toDomain(): TodayFocus =
    TodayFocus(text = text, dayStart = dayStart.toApiInstant("focus.day_start"))

internal fun TodayDto.toDomain(): TodaySnapshot = TodaySnapshot(
    focus = focus?.toDomain(),
    topTasks = topTasks.map {
        TodayTask(projectId = it.project, projectName = it.projectName, taskId = it.task, title = it.title)
    },
    completedToday = TodayCompletion(
        all = completedToday.all,
        top = completedToday.top,
        dayStart = completedToday.dayStart.toApiInstant("completed_today.day_start"),
        dayEnd = completedToday.dayEnd.toApiInstant("completed_today.day_end"),
    ),
)
internal fun CommentRequest.toWire(): CommentDto = CommentDto(body)
internal fun CloseTaskRequest.toWire(): CloseTaskDto = CloseTaskDto(message, evidence, comment)

internal fun CreateViewRequest.toWire(): CreateViewDto = CreateViewDto(name, query.toWire(), order?.toWire())

internal fun UpdateViewRequest.toWire(): ViewPatchDto = ViewPatchDto(
    name = name,
    query = query?.toWire(),
    order = order?.toWire(),
    presentFields = presentFields(),
)

internal fun String.toApiInstantOrNull(field: String): Instant? = if (isBlank()) null else toApiInstant(field)

package facets.mobile.data.cache

import facets.mobile.data.model.Project
import facets.mobile.data.model.SavedView
import facets.mobile.data.model.Task
import facets.mobile.data.model.TaskStatus
import facets.mobile.data.model.ViewOrder
import facets.mobile.data.model.ViewOrderDirection
import facets.mobile.data.model.ViewOrderField
import facets.mobile.data.model.ViewQuery
import facets.mobile.data.remote.ProjectDto
import facets.mobile.data.remote.SavedViewDto
import facets.mobile.data.remote.TaskDto
import facets.mobile.data.remote.facetsJson
import facets.mobile.data.remote.toDomain
import java.time.Instant
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString

internal fun ProjectDto.toEntity(): ProjectEntity = toDomain().toEntity()

internal fun TaskDto.toEntity(): TaskEntity = toDomain().toEntity()

internal fun SavedViewDto.toEntity(): SavedViewEntity = toDomain().toEntity()

internal fun Project.toEntity(): ProjectEntity = ProjectEntity(
    id = id,
    name = name,
    description = description,
    activeTaskCount = activeTaskCount,
    createdAtEpochMillis = createdAt.toEpochMilli(),
    updatedAtEpochMillis = updatedAt?.toEpochMilli(),
)

internal fun Task.toEntity(): TaskEntity = TaskEntity(
    id = id,
    projectId = projectId,
    title = title,
    description = description,
    status = status.name,
    priority = priority,
    assignee = assignee,
    createdAtEpochMillis = createdAt.toEpochMilli(),
    updatedAtEpochMillis = updatedAt.toEpochMilli(),
    top = top,
)

internal fun SavedView.toEntity(): SavedViewEntity = SavedViewEntity(
    id = id,
    name = name,
    builtin = builtin,
    statusesJson = facetsJson.encodeToString(query.statuses.map { it.name }),
    assigneesJson = facetsJson.encodeToString(query.assignees),
    prioritiesJson = facetsJson.encodeToString(query.priorities),
    orderField = order.field.name,
    orderDirection = order.direction.name,
    createdAtEpochMillis = createdAt?.toEpochMilli(),
    updatedAtEpochMillis = updatedAt?.toEpochMilli(),
)

internal fun ProjectEntity.toDomain(): Project = Project(
    id = id,
    name = name,
    description = description,
    activeTaskCount = activeTaskCount,
    createdAt = Instant.ofEpochMilli(createdAtEpochMillis),
    updatedAt = updatedAtEpochMillis?.let(Instant::ofEpochMilli),
)

internal fun TaskEntity.toDomain(): Task = Task(
    id = id,
    projectId = projectId,
    title = title,
    description = description,
    status = TaskStatus.valueOf(status),
    priority = priority,
    assignee = assignee,
    createdAt = Instant.ofEpochMilli(createdAtEpochMillis),
    updatedAt = Instant.ofEpochMilli(updatedAtEpochMillis),
    top = top,
)

internal fun SavedViewEntity.toDomain(): SavedView = SavedView(
    id = id,
    name = name,
    builtin = builtin,
    query = ViewQuery(
        statuses = facetsJson.decodeFromString<List<String>>(statusesJson).map(TaskStatus::valueOf),
        assignees = facetsJson.decodeFromString(assigneesJson),
        priorities = facetsJson.decodeFromString(prioritiesJson),
    ),
    order = ViewOrder(
        field = ViewOrderField.valueOf(orderField),
        direction = ViewOrderDirection.valueOf(orderDirection),
    ),
    createdAt = createdAtEpochMillis?.let(Instant::ofEpochMilli),
    updatedAt = updatedAtEpochMillis?.let(Instant::ofEpochMilli),
)

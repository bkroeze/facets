package facets.mobile.data.remote

import facets.mobile.data.transport.UnknownApiEnumException
import kotlinx.serialization.KSerializer
import kotlinx.serialization.Serializable
import kotlinx.serialization.SerializationException
import kotlinx.serialization.descriptors.PrimitiveKind
import kotlinx.serialization.descriptors.PrimitiveSerialDescriptor
import kotlinx.serialization.descriptors.SerialDescriptor
import kotlinx.serialization.descriptors.buildClassSerialDescriptor
import kotlinx.serialization.descriptors.element
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder
import kotlinx.serialization.json.JsonDecoder
import kotlinx.serialization.json.JsonEncoder
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.decodeFromJsonElement
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import kotlinx.serialization.json.int
import kotlinx.serialization.SerialName

@Serializable
data class ApiRootDto(val version: String)

@Serializable
data class ProjectDto(
    val id: String,
    val name: String,
    val description: String,
    @SerialName("active_task_count") val activeTaskCount: Int,
    @SerialName("created_at") val createdAt: String,
    @SerialName("updated_at") val updatedAt: String?,
)

@Serializable
data class ProjectListDto(val projects: List<ProjectDto>)

@Serializable(with = TaskStatusDtoSerializer::class)
enum class TaskStatusDto(val wireValue: String) {
    OPEN("open"),
    CLOSED("closed"),
}

object TaskStatusDtoSerializer : KSerializer<TaskStatusDto> {
    override val descriptor: SerialDescriptor = PrimitiveSerialDescriptor("TaskStatus", PrimitiveKind.STRING)
    override fun serialize(encoder: Encoder, value: TaskStatusDto) = encoder.encodeString(value.wireValue)
    override fun deserialize(decoder: Decoder): TaskStatusDto = when (val value = decoder.decodeString()) {
        "open" -> TaskStatusDto.OPEN
        "closed" -> TaskStatusDto.CLOSED
        else -> throw UnknownApiEnumException("Unknown task status '$value'")
    }
}

@Serializable
data class TaskDto(
    val id: String,
    @SerialName("project_id") val projectId: String,
    val title: String,
    val description: String,
    val status: TaskStatusDto,
    val priority: Int?,
    val assignee: String,
    val top: Boolean = false,
    @SerialName("created_at") val createdAt: String,
    @SerialName("updated_at") val updatedAt: String,
)

@Serializable
data class TaskListDto(val tasks: List<TaskDto>)

@Serializable
data class CreateTaskDto(
    val title: String,
    val description: String? = null,
    val priority: Int? = null,
    val assignee: String? = null,
    @SerialName("idempotency_key") val idempotencyKey: String? = null,
)

@Serializable(with = TaskPatchDtoSerializer::class)
data class TaskPatchDto(
    val title: String? = null,
    val description: String? = null,
    val priority: Int? = null,
    val assignee: String? = null,
    val top: Boolean? = null,
    @kotlinx.serialization.Transient val presentFields: Set<String> = emptySet(),
)

object TaskPatchDtoSerializer : KSerializer<TaskPatchDto> {
    override val descriptor: SerialDescriptor = buildClassSerialDescriptor("TaskPatch") {
        element<String?>("title", isOptional = true)
        element<String?>("description", isOptional = true)
        element<Int?>("priority", isOptional = true)
        element<String?>("assignee", isOptional = true)
        element<Boolean>("top", isOptional = true)
    }

    override fun serialize(encoder: Encoder, value: TaskPatchDto) {
        val jsonEncoder = encoder as? JsonEncoder
            ?: throw SerializationException("Task patches require a JSON encoder")
        val fields = value.presentFields
        jsonEncoder.encodeJsonElement(buildJsonObject {
            if ("title" in fields || value.title != null) putNullable("title", value.title)
            if ("description" in fields || value.description != null) putNullable("description", value.description)
            if ("priority" in fields || value.priority != null) putNullable("priority", value.priority)
            if ("assignee" in fields || value.assignee != null) putNullable("assignee", value.assignee)
            if ("top" in fields || value.top != null) {
                val top = value.top ?: throw SerializationException("top cannot be null")
                put("top", top)
            }
        })
    }

    override fun deserialize(decoder: Decoder): TaskPatchDto {
        val jsonDecoder = decoder as? JsonDecoder
            ?: throw SerializationException("Task patches require a JSON decoder")
        val objectValue = jsonDecoder.decodeJsonElement().jsonObject
        fun string(name: String): String? = objectValue[name]?.takeUnless { it is JsonNull }?.jsonPrimitive?.content
        fun integer(name: String): Int? = objectValue[name]?.takeUnless { it is JsonNull }?.jsonPrimitive?.int
        fun boolean(name: String): Boolean? = objectValue[name]?.takeUnless { it is JsonNull }?.jsonPrimitive?.content?.toBooleanStrictOrNull()
        return TaskPatchDto(
            title = string("title"),
            description = string("description"),
            priority = integer("priority"),
            assignee = string("assignee"),
            top = boolean("top"),
            presentFields = objectValue.keys,
        )
    }
}

@Serializable
data class CommentDto(val body: String)
@Serializable
data class TodayFocusDto(
    val text: String,
    @SerialName("day_start") val dayStart: String,
)

@Serializable
data class TodayTaskDto(
    val project: String,
    @SerialName("project_name") val projectName: String,
    val task: String,
    val title: String,
)

@Serializable
data class TodayCompletedDto(
    val all: Int,
    val top: Int,
    @SerialName("day_start") val dayStart: String,
    @SerialName("day_end") val dayEnd: String,
)

@Serializable
data class TodayDto(
    val focus: TodayFocusDto?,
    @SerialName("top_tasks") val topTasks: List<TodayTaskDto>,
    @SerialName("completed_today") val completedToday: TodayCompletedDto,
)

@Serializable
data class SetTodayFocusDto(val text: String)
@Serializable
data class TodayFocusResponseDto(val focus: TodayFocusDto)

@Serializable
data class CloseTaskDto(
    val message: String,
    val evidence: List<String>,
    val comment: String? = null,
)

@Serializable
data class DeleteTaskDto(val confirm: String)

@Serializable(with = ViewOrderFieldDtoSerializer::class)
enum class ViewOrderFieldDto(val wireValue: String) {
    TITLE("title"), STATUS("status"), PRIORITY("priority"), ASSIGNEE("assignee"),
    CREATED_AT("created_at"), UPDATED_AT("updated_at"),
}

object ViewOrderFieldDtoSerializer : KSerializer<ViewOrderFieldDto> {
    override val descriptor: SerialDescriptor = PrimitiveSerialDescriptor("ViewOrderField", PrimitiveKind.STRING)
    override fun serialize(encoder: Encoder, value: ViewOrderFieldDto) = encoder.encodeString(value.wireValue)
    override fun deserialize(decoder: Decoder): ViewOrderFieldDto = when (val value = decoder.decodeString()) {
        "title" -> ViewOrderFieldDto.TITLE
        "status" -> ViewOrderFieldDto.STATUS
        "priority" -> ViewOrderFieldDto.PRIORITY
        "assignee" -> ViewOrderFieldDto.ASSIGNEE
        "created_at" -> ViewOrderFieldDto.CREATED_AT
        "updated_at" -> ViewOrderFieldDto.UPDATED_AT
        else -> throw UnknownApiEnumException("Unknown view order field '$value'")
    }
}

@Serializable(with = ViewOrderDirectionDtoSerializer::class)
enum class ViewOrderDirectionDto(val wireValue: String) { ASC("asc"), DESC("desc") }

object ViewOrderDirectionDtoSerializer : KSerializer<ViewOrderDirectionDto> {
    override val descriptor: SerialDescriptor = PrimitiveSerialDescriptor("ViewOrderDirection", PrimitiveKind.STRING)
    override fun serialize(encoder: Encoder, value: ViewOrderDirectionDto) = encoder.encodeString(value.wireValue)
    override fun deserialize(decoder: Decoder): ViewOrderDirectionDto = when (val value = decoder.decodeString()) {
        "asc" -> ViewOrderDirectionDto.ASC
        "desc" -> ViewOrderDirectionDto.DESC
        else -> throw UnknownApiEnumException("Unknown view order direction '$value'")
    }
}

@Serializable
data class ViewQueryDto(
    val statuses: List<TaskStatusDto>,
    val assignees: List<String>,
    val priorities: List<Int>,
)

@Serializable
data class ViewOrderDto(
    val field: ViewOrderFieldDto,
    val direction: ViewOrderDirectionDto,
)

@Serializable
data class SavedViewDto(
    val id: String,
    val name: String,
    val builtin: Boolean,
    val query: ViewQueryDto,
    val order: ViewOrderDto,
    @SerialName("created_at") val createdAt: String?,
    @SerialName("updated_at") val updatedAt: String?,
)

@Serializable
data class SavedViewListDto(val views: List<SavedViewDto>)

@Serializable
data class CreateViewDto(
    val name: String,
    val query: ViewQueryDto,
    val order: ViewOrderDto? = null,
)

@Serializable(with = ViewPatchDtoSerializer::class)
data class ViewPatchDto(
    val name: String? = null,
    val query: ViewQueryDto? = null,
    val order: ViewOrderDto? = null,
    @kotlinx.serialization.Transient val presentFields: Set<String> = emptySet(),
)
object ViewPatchDtoSerializer : KSerializer<ViewPatchDto> {
    override val descriptor: SerialDescriptor = buildClassSerialDescriptor("ViewPatch") {
        element<String?>("name", isOptional = true)
        element<ViewQueryDto?>("query", isOptional = true)
        element<ViewOrderDto?>("order", isOptional = true)
    }

    override fun serialize(encoder: Encoder, value: ViewPatchDto) {
        val jsonEncoder = encoder as? JsonEncoder
            ?: throw SerializationException("View patches require a JSON encoder")
        val fields = value.presentFields
        jsonEncoder.encodeJsonElement(buildJsonObject {
            if ("name" in fields || value.name != null) putNullable("name", value.name)
            if ("query" in fields || value.query != null) putNullable("query", value.query)
            if ("order" in fields || value.order != null) putNullable("order", value.order)
        })
    }

    override fun deserialize(decoder: Decoder): ViewPatchDto {
        val jsonDecoder = decoder as? JsonDecoder
            ?: throw SerializationException("View patches require a JSON decoder")
        val objectValue = jsonDecoder.decodeJsonElement().jsonObject
        val json = jsonDecoder.json
        return ViewPatchDto(
            name = objectValue["name"]?.takeUnless { it is JsonNull }?.jsonPrimitive?.content,
            query = objectValue["query"]?.takeUnless { it is JsonNull }?.let { json.decodeFromJsonElement<ViewQueryDto>(it) },
            order = objectValue["order"]?.takeUnless { it is JsonNull }?.let { json.decodeFromJsonElement<ViewOrderDto>(it) },
            presentFields = objectValue.keys,
        )
    }
}

@Serializable
data class ErrorDetailsDto(val fields: Map<String, String> = emptyMap())

@Serializable(with = ApiErrorCodeDtoSerializer::class)
enum class ApiErrorCodeDto(val wireValue: String) {
    INVALID_REQUEST("invalid_request"), VALIDATION_FAILED("validation_failed"), NOT_FOUND("not_found"),
    METHOD_NOT_ALLOWED("method_not_allowed"), NOT_ACCEPTABLE("not_acceptable"),
    CONFLICT("conflict"), UNSUPPORTED_MEDIA_TYPE("unsupported_media_type"),
    REQUEST_CANCELED("request_canceled"), UNSUPPORTED_OPERATION("unsupported_operation"),
    PROVIDER_FAILURE("provider_failure"), PROVIDER_UNAVAILABLE("provider_unavailable"),
    PROVIDER_TIMEOUT("provider_timeout"), INTERNAL_ERROR("internal_error"),
}

object ApiErrorCodeDtoSerializer : KSerializer<ApiErrorCodeDto> {
    override val descriptor: SerialDescriptor = PrimitiveSerialDescriptor("ApiErrorCode", PrimitiveKind.STRING)
    override fun serialize(encoder: Encoder, value: ApiErrorCodeDto) = encoder.encodeString(value.wireValue)
    override fun deserialize(decoder: Decoder): ApiErrorCodeDto = when (val value = decoder.decodeString()) {
        "invalid_request" -> ApiErrorCodeDto.INVALID_REQUEST
        "validation_failed" -> ApiErrorCodeDto.VALIDATION_FAILED
        "not_found" -> ApiErrorCodeDto.NOT_FOUND
        "method_not_allowed" -> ApiErrorCodeDto.METHOD_NOT_ALLOWED
        "not_acceptable" -> ApiErrorCodeDto.NOT_ACCEPTABLE
        "conflict" -> ApiErrorCodeDto.CONFLICT
        "unsupported_media_type" -> ApiErrorCodeDto.UNSUPPORTED_MEDIA_TYPE
        "request_canceled" -> ApiErrorCodeDto.REQUEST_CANCELED
        "unsupported_operation" -> ApiErrorCodeDto.UNSUPPORTED_OPERATION
        "provider_failure" -> ApiErrorCodeDto.PROVIDER_FAILURE
        "provider_unavailable" -> ApiErrorCodeDto.PROVIDER_UNAVAILABLE
        "provider_timeout" -> ApiErrorCodeDto.PROVIDER_TIMEOUT
        "internal_error" -> ApiErrorCodeDto.INTERNAL_ERROR
        else -> throw UnknownApiEnumException("Unknown API error code '$value'")
    }
}

@Serializable
data class ErrorDto(
    val code: ApiErrorCodeDto,
    val message: String,
    @SerialName("request_id") val requestId: String,
    val details: ErrorDetailsDto? = null,
)

@Serializable
data class ErrorEnvelopeDto(val error: ErrorDto)

private fun kotlinx.serialization.json.JsonObjectBuilder.putNullable(name: String, value: String?) {
    put(name, value?.let(::JsonPrimitive) ?: JsonNull)
}

private fun kotlinx.serialization.json.JsonObjectBuilder.putNullable(name: String, value: Int?) {
    put(name, value?.let(::JsonPrimitive) ?: JsonNull)
}

private fun kotlinx.serialization.json.JsonObjectBuilder.putNullable(name: String, value: ViewQueryDto?) {
    put(name, value?.let { kotlinx.serialization.json.Json.encodeToJsonElement(ViewQueryDto.serializer(), it) } ?: JsonNull)
}

private fun kotlinx.serialization.json.JsonObjectBuilder.putNullable(name: String, value: ViewOrderDto?) {
    put(name, value?.let { kotlinx.serialization.json.Json.encodeToJsonElement(ViewOrderDto.serializer(), it) } ?: JsonNull)
}

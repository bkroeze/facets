package facets.mobile.data.remote

import okhttp3.ResponseBody
import retrofit2.Response
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.HTTP
import retrofit2.http.GET
import retrofit2.http.PATCH
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query
interface FacetsApi {
    @GET("api/v1")
    suspend fun apiRoot(): Response<ApiRootDto>

    @GET("healthz")
    suspend fun health(): Response<ResponseBody>
    @GET("api/v1/today")
    suspend fun today(): Response<TodayDto>
    @POST("api/v1/today/focus")
    suspend fun setTodayFocus(@Body request: SetTodayFocusDto): Response<TodayFocusResponseDto>
    @GET("api/v1/projects")
    suspend fun listProjects(): Response<ProjectListDto>

    @GET("api/v1/projects/{project_id}")
    suspend fun getProject(@Path("project_id") projectId: String): Response<ProjectDto>

    @GET("api/v1/projects/{project_id}/tasks")
    suspend fun listTasks(
        @Path("project_id") projectId: String,
        @Query("view") viewId: String? = null,
        @Query("status") status: String? = null,
    ): Response<TaskListDto>

    @GET("api/v1/projects/{project_id}/tasks/{task_id}")
    suspend fun getTask(
        @Path("project_id") projectId: String,
        @Path("task_id") taskId: String,
    ): Response<TaskDto>

    @POST("api/v1/projects/{project_id}/tasks")
    suspend fun createTask(
        @Path("project_id") projectId: String,
        @Body request: CreateTaskDto,
    ): Response<TaskDto>

    @PATCH("api/v1/projects/{project_id}/tasks/{task_id}")
    suspend fun updateTask(
        @Path("project_id") projectId: String,
        @Path("task_id") taskId: String,
        @Body request: TaskPatchDto,
    ): Response<TaskDto>

    @POST("api/v1/projects/{project_id}/tasks/{task_id}/comments")
    suspend fun commentTask(
        @Path("project_id") projectId: String,
        @Path("task_id") taskId: String,
        @Body request: CommentDto,
    ): Response<TaskDto>

    @POST("api/v1/projects/{project_id}/tasks/{task_id}/close")
    suspend fun closeTask(
        @Path("project_id") projectId: String,
        @Path("task_id") taskId: String,
        @Body request: CloseTaskDto,
    ): Response<TaskDto>

    @POST("api/v1/projects/{project_id}/tasks/{task_id}/reopen")
    suspend fun reopenTask(
        @Path("project_id") projectId: String,
        @Path("task_id") taskId: String,
    ): Response<TaskDto>

    @HTTP(method = "DELETE", path = "api/v1/projects/{project_id}/tasks/{task_id}", hasBody = true)
    suspend fun deleteTask(
        @Path("project_id") projectId: String,
        @Path("task_id") taskId: String,
        @Body request: DeleteTaskDto,
    ): Response<Unit>

    @GET("api/v1/views")
    suspend fun listViews(): Response<SavedViewListDto>

    @POST("api/v1/views")
    suspend fun createView(@Body request: CreateViewDto): Response<SavedViewDto>

    @GET("api/v1/views/{view_id}")
    suspend fun getView(@Path("view_id") viewId: String): Response<SavedViewDto>

    @PATCH("api/v1/views/{view_id}")
    suspend fun updateView(
        @Path("view_id") viewId: String,
        @Body request: ViewPatchDto,
    ): Response<SavedViewDto>

    @DELETE("api/v1/views/{view_id}")
    suspend fun deleteView(@Path("view_id") viewId: String): Response<Unit>

    @GET("api/v1/projects/{project_id}/views/{view_id}/tasks")
    suspend fun listTasksForView(
        @Path("project_id") projectId: String,
        @Path("view_id") viewId: String,
    ): Response<TaskListDto>
}

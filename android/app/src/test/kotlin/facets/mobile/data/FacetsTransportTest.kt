package facets.mobile.data

import facets.mobile.data.model.CloseTaskRequest
import facets.mobile.data.model.CommentRequest
import facets.mobile.data.model.CreateTaskRequest
import facets.mobile.data.model.CreateViewRequest
import facets.mobile.data.model.TaskListStatus
import facets.mobile.data.model.TaskUpdateField
import facets.mobile.data.model.UpdateTaskRequest
import facets.mobile.data.model.UpdateViewRequest
import facets.mobile.data.transport.FacetsClient
import facets.mobile.data.transport.FacetsClientFactory
import facets.mobile.data.transport.FacetsException
import facets.mobile.data.transport.FacetsFailure
import facets.mobile.data.transport.InvalidServerUrlException
import facets.mobile.data.transport.ServerUrlValidator
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import okhttp3.tls.HandshakeCertificates
import okhttp3.tls.HeldCertificate
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.util.concurrent.TimeUnit

class FacetsTransportTest {
    private lateinit var server: MockWebServer
    private lateinit var client: FacetsClient
    private lateinit var trustedClient: OkHttpClient

    @Before
    fun setUp() {
        val certificate = HeldCertificate.Builder()
            .addSubjectAlternativeName("localhost")
            .build()
        val serverCertificates = HandshakeCertificates.Builder().heldCertificate(certificate).build()
        val clientCertificates = HandshakeCertificates.Builder()
            .addTrustedCertificate(certificate.certificate)
            .build()
        server = MockWebServer()
        server.useHttps(serverCertificates.sslSocketFactory(), false)
        server.start()
        val secureClient = OkHttpClient.Builder()
            .sslSocketFactory(clientCertificates.sslSocketFactory(), clientCertificates.trustManager)
            .build()
        trustedClient = secureClient
        client = FacetsClientFactory.createForTesting(server.url("/").toString(), httpClient = secureClient)
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    @Test
    fun urlValidationRejectsCleartextMalformedCredentialsAndExtraComponents() {
        assertEquals("https://tailnet.example/", ServerUrlValidator.validate(" https://tailnet.example ").toString())
        listOf(
            "http://tailnet.example",
            "not a URL",
            "https://user:pass@tailnet.example",
            "https://tailnet.example/api",
            "https://tailnet.example/?token=secret",
            "https://tailnet.example/#fragment",
        ).forEach { value ->
            try {
                ServerUrlValidator.validate(value)
                throw AssertionError("Expected URL rejection for $value")
            } catch (_: InvalidServerUrlException) {
                // expected
            }
        }
    }

    @Test
    fun allResourceAndLifecycleMappingsUseV1Routes() = runBlocking {
        server.enqueue(ok(projectListJson))
        server.enqueue(ok(projectJson))
        server.enqueue(ok(taskListJson))
        repeat(6) { server.enqueue(ok(taskJson)) }
        server.enqueue(MockResponse().setResponseCode(204))
        server.enqueue(ok(viewListJson))
        repeat(3) { server.enqueue(ok(viewJson)) }
        server.enqueue(MockResponse().setResponseCode(204))
        server.enqueue(ok(taskListJson))

        assertEquals(1, client.listProjects().size)
        assertEquals("facets", client.getProject("facets").id)
        assertEquals(1, client.listTasks("facets", status = TaskListStatus.OPEN).size)
        assertEquals("ab12", client.getTask("facets", "ab12").id)
        assertEquals("ab12", client.createTask("facets", CreateTaskRequest("Add Android API")).id)
        assertEquals(
            "ab12",
            client.updateTask(
                "facets",
                "ab12",
                UpdateTaskRequest(priority = null, fields = setOf(TaskUpdateField.PRIORITY)),
            ).id,
        )
        assertEquals("ab12", client.commentTask("facets", "ab12", CommentRequest("done")).id)
        assertEquals("ab12", client.closeTask("facets", "ab12", CloseTaskRequest("done", listOf("test:ok"))).id)
        assertEquals("ab12", client.reopenTask("facets", "ab12").id)
        client.deleteTask("facets", "ab12")
        assertEquals(1, client.listViews().size)
        assertEquals("assigned", client.createView(CreateViewRequest("assigned")).id)
        assertEquals("assigned", client.getView("assigned").id)
        assertEquals("assigned", client.updateView("assigned", UpdateViewRequest(name = "updated")).id)
        client.deleteView("assigned")
        assertEquals(1, client.listTasksForView("facets", "assigned").size)

        val expected = listOf(
            "GET /api/v1/projects",
            "GET /api/v1/projects/facets",
            "GET /api/v1/projects/facets/tasks?status=open",
            "GET /api/v1/projects/facets/tasks/ab12",
            "POST /api/v1/projects/facets/tasks",
            "PATCH /api/v1/projects/facets/tasks/ab12",
            "POST /api/v1/projects/facets/tasks/ab12/comments",
            "POST /api/v1/projects/facets/tasks/ab12/close",
            "POST /api/v1/projects/facets/tasks/ab12/reopen",
            "DELETE /api/v1/projects/facets/tasks/ab12",
            "GET /api/v1/views",
            "POST /api/v1/views",
            "GET /api/v1/views/assigned",
            "PATCH /api/v1/views/assigned",
            "DELETE /api/v1/views/assigned",
            "GET /api/v1/projects/facets/views/assigned/tasks",
        )
        val requests = expected.map { server.takeRequest() }
        requests.forEachIndexed { index, request ->
            assertEquals(expected[index], "${request.method} ${request.path}")
        }
        val patchRequest = requests[5]
        assertTrue(patchRequest.body.readUtf8().contains("\"priority\":null"))
        val deleteRequest = requests[9]
        assertTrue(deleteRequest.body.readUtf8().contains("\"confirm\":\"ab12\""))
    }

    @Test
    fun versionMismatchUnknownEnumAndMalformedJsonAreTyped() = runBlocking {
        server.enqueue(ok("{\"version\":\"v2\"}"))
        try {
            client.apiVersion()
            throw AssertionError("Expected incompatible contract")
        } catch (error: FacetsException) {
            assertTrue(error.failure is FacetsFailure.IncompatibleContract)
        }

        server.enqueue(ok("{\"projects\":[{"))
        try {
            client.listProjects()
            throw AssertionError("Expected malformed response")
        } catch (error: FacetsException) {
            assertTrue(error.failure is FacetsFailure.MalformedResponse)
        }

        server.enqueue(ok(taskListJson.replace("\"open\"", "\"paused\"")))
        try {
            client.listTasks("facets")
            throw AssertionError("Expected incompatible enum")
        } catch (error: FacetsException) {
            assertTrue(error.failure is FacetsFailure.IncompatibleContract)
        }
    }

    @Test
    fun httpErrorsMapToDistinctFailuresAndPreserveEnvelopeFields() = runBlocking {
        val cases = listOf(
            400 to FacetsFailure.InvalidRequest::class,
            404 to FacetsFailure.NotFound::class,
            409 to FacetsFailure.Conflict::class,
            503 to FacetsFailure.ServerFailure::class,
        )
        cases.forEach { (status, expectedType) ->
            server.enqueue(
                MockResponse()
                    .setResponseCode(status)
                    .setHeader("Content-Type", "application/json")
                    .setBody(errorJson),
            )
            try {
                client.listProjects()
                throw AssertionError("Expected HTTP $status failure")
            } catch (error: FacetsException) {
                assertTrue(expectedType.isInstance(error.failure))
                if (error.failure is FacetsFailure.InvalidRequest) {
                    assertEquals("must not be empty", error.failure.fields["title"])
                }
            }
        }
    }

    @Test
    fun timeoutAndLocalCancellationRemainDistinct() = runBlocking {
        repeat(2) {
            server.enqueue(
                MockResponse()
                    .setHeader("Content-Type", "application/json")
                    .setBody(projectListJson)
                    .setBodyDelay(5, TimeUnit.SECONDS),
            )
        }
        val shortTimeoutClient = FacetsClientFactory.createForTesting(
            server.url("/").toString(),
            readTimeoutMillis = 2_000,
            httpClient = secureHttpClient(),
        )
        try {
            shortTimeoutClient.listProjects()
            throw AssertionError("Expected timeout")
        } catch (error: FacetsException) {
            assertTrue("actual failure: ${error.failure}", error.failure is FacetsFailure.Timeout)
        }

        val job: Job = launch {
            try {
                client.listProjects()
                throw AssertionError("Expected cancellation")
            } catch (error: CancellationException) {
                throw error
            }
        }
        delay(20)
        job.cancel()
        job.join()
        assertTrue(job.isCancelled)
    }

    private fun secureHttpClient(): OkHttpClient = trustedClient
    private fun delayedDispatcher(delayMillis: Long) = object : Dispatcher() {
        override fun dispatch(request: RecordedRequest): MockResponse = MockResponse()
            .setHeader("Content-Type", "application/json")
            .setBody(projectListJson)
            .setBodyDelay(delayMillis, TimeUnit.MILLISECONDS)
    }

    private fun ok(body: String) = MockResponse()
        .setHeader("Content-Type", "application/json")
        .setBody(body)

    private companion object {
        const val projectJson = "{\"id\":\"facets\",\"name\":\"Facets\",\"description\":\"Dashboard\",\"active_task_count\":1,\"created_at\":\"2026-08-12T12:00:00Z\",\"updated_at\":null}"
        const val projectListJson = "{\"projects\":[$projectJson]}"
        const val taskJson = "{\"id\":\"ab12\",\"project_id\":\"facets\",\"title\":\"Add Android API\",\"description\":\"Define transport\",\"status\":\"open\",\"priority\":2,\"assignee\":\"bruce\",\"created_at\":\"2026-08-12T12:00:00Z\",\"updated_at\":\"2026-08-12T12:30:00Z\"}"
        const val taskListJson = "{\"tasks\":[$taskJson]}"
        const val viewJson = "{\"id\":\"assigned\",\"name\":\"Assigned\",\"builtin\":false,\"query\":{\"statuses\":[\"open\"],\"assignees\":[\"bruce\"],\"priorities\":[1]},\"order\":{\"field\":\"priority\",\"direction\":\"asc\"},\"created_at\":\"2026-08-12T12:00:00Z\",\"updated_at\":null}"
        const val viewListJson = "{\"views\":[$viewJson]}"
        const val errorJson = "{\"error\":{\"code\":\"validation_failed\",\"message\":\"invalid\",\"request_id\":\"42\",\"details\":{\"fields\":{\"title\":\"must not be empty\"}}}}"
    }
}

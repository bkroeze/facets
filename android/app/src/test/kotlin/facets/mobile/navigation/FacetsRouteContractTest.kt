package facets.mobile.navigation

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class FacetsRouteContractTest {
    @Test
    fun parsesProjectAndTaskLinksAndBuildsEncodedRoutes() {
        val project = FacetsRouteContract.parse("facets://projects/project%20one")
        assertEquals(FacetsRouteParseResult.Valid(FacetsDestination.Project("project one")), project)

        val task = FacetsRouteContract.parse("facets://projects/project%20one/tasks/task%20one")
        assertEquals(FacetsRouteParseResult.Valid(FacetsDestination.Task("project one", "task one")), task)
        assertEquals(
            "projects/project%20one/tasks/task%20one",
            FacetsRouteContract.taskNavigationRoute("project one", "task one"),
        )
    }

    @Test
    fun rejectsMalformedAndEscapingLinks() {
        listOf(
            "facets://projects",
            "facets://projects/../tasks/task",
            "facets://projects/project/tasks/task?redirect=https://evil.example",
            "facets://projects/project%2Fother",
            "https://projects/project",
        ).forEach { value ->
            assertTrue(FacetsRouteContract.parse(value) is FacetsRouteParseResult.Invalid)
        }
        assertFalse(FacetsRouteContract.isSafeOpaqueId("."))
        assertFalse(FacetsRouteContract.isSafeOpaqueId("../escape"))
    }
}

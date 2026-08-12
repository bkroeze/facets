package facets.mobile.navigation

import org.junit.Assert.assertEquals
import org.junit.Test

class TopLevelDestinationTest {
    @Test
    fun restoresEachSavedDestination() {
        TopLevelDestination.entries.forEach { destination ->
            assertEquals(destination, restoreTopLevelDestination(destination.name))
        }
    }

    @Test
    fun invalidOrMissingStateFallsBackToHome() {
        assertEquals(TopLevelDestination.HOME, restoreTopLevelDestination(null))
        assertEquals(TopLevelDestination.HOME, restoreTopLevelDestination("removed_destination"))
    }
}

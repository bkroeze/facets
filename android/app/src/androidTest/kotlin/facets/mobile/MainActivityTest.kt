package facets.mobile

import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class MainActivityTest {
    @get:Rule
    val composeRule = createAndroidComposeRule<MainActivity>()

    @Test
    fun launchesTodayAndRestoresSelectedDestinationAfterRecreation() {
        composeRule.onNodeWithText("Connect a server in Settings to see today's focus and top tasks.").fetchSemanticsNode()
        composeRule.onNodeWithText("Tasks").performClick()
        composeRule.onNodeWithText("Tasks will appear here").fetchSemanticsNode()

        composeRule.activityRule.scenario.recreate()
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Tasks will appear here").fetchSemanticsNode()
    }
}

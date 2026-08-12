package facets.mobile

import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.createAndroidComposeRule
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
    fun launchesHomeAndRestoresSelectedDestinationAfterRecreation() {
        composeRule.onNodeWithText("Your dashboard at a glance").assertIsDisplayed()
        composeRule.onNodeWithText("Tasks").performClick()
        composeRule.onNodeWithText("Tasks will appear here").assertIsDisplayed()

        composeRule.activityRule.scenario.recreate()
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Tasks will appear here").assertIsDisplayed()
    }
}

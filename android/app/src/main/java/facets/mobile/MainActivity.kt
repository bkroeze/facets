package facets.mobile

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import facets.mobile.navigation.FacetsApp
import facets.mobile.ui.theme.FacetsTheme

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            FacetsTheme {
                FacetsApp()
            }
        }
    }
}

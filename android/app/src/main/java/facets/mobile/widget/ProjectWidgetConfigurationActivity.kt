package facets.mobile.widget

import android.appwidget.AppWidgetManager
import android.content.Intent
import android.os.Bundle
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import androidx.activity.ComponentActivity

private const val PREFS = "project_widget"
private const val SELECTED_PREFIX = "selected_"

class ProjectWidgetConfigurationActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val widgetId = intent.getIntExtra(AppWidgetManager.EXTRA_APPWIDGET_ID, AppWidgetManager.INVALID_APPWIDGET_ID)
        if (widgetId == AppWidgetManager.INVALID_APPWIDGET_ID) {
            setResult(RESULT_CANCELED)
            finish()
            return
        }
        val input = EditText(this).apply {
            hint = "Project IDs, comma separated (blank = all)"
            setSingleLine(false)
        }
        val content = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(32, 32, 32, 32)
            addView(TextView(context).apply { text = "Choose projects for this widget" })
            addView(input)
            addView(Button(context).apply {
                text = "Save"
                setOnClickListener {
                    val ids = input.text.toString().split(',').map(String::trim).filter(String::isNotEmpty).toSet()
                    getSharedPreferences(PREFS, MODE_PRIVATE).edit().putStringSet("$SELECTED_PREFIX$widgetId", ids).apply()
                    setResult(RESULT_OK, Intent().putExtra(AppWidgetManager.EXTRA_APPWIDGET_ID, widgetId))
                    finish()
                }
            })
        }
        setResult(RESULT_CANCELED)
        setContentView(content)
    }
}

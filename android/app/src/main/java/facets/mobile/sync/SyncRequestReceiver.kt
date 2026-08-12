package facets.mobile.sync

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import facets.mobile.FacetsApplication

/** Handles system lifecycle and widget refresh requests without doing network work in onReceive. */
class SyncRequestReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val scheduler = (context.applicationContext as? FacetsApplication)?.syncScheduler ?: return
        when (intent.action) {
            Intent.ACTION_BOOT_COMPLETED,
            Intent.ACTION_MY_PACKAGE_REPLACED,
            ACTION_WIDGET_REFRESH -> scheduler.enqueueImmediate(intent.action ?: "broadcast")
        }
    }

    companion object {
        const val ACTION_WIDGET_REFRESH = "facets.mobile.sync.WIDGET_REFRESH"
    }
}

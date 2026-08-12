package facets.mobile.settings

import android.content.Context
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.intPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore

import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

private val Context.serverSettingsDataStore: DataStore<Preferences> by preferencesDataStore(
    name = "server_settings",
)
data class SyncSettings(
    val enabled: Boolean = true,
    val intervalMinutes: Long = 60L,
)

const val MIN_SYNC_INTERVAL_MINUTES: Long = 15L
const val DEFAULT_SYNC_INTERVAL_MINUTES: Long = 60L

/** Persistent user preferences for the server this app talks to. */
class ServerSettingsStore(
    context: Context,
    private val validateBaseUrl: (String) -> Result<String>,
) {
    private val dataStore = context.applicationContext.serverSettingsDataStore

    val baseUrl: Flow<String?> = dataStore.data.map { preferences ->
        preferences[BASE_URL_KEY]
    }

    val syncSettings: Flow<SyncSettings> = dataStore.data.map { preferences ->
        SyncSettings(
            enabled = preferences[SYNC_ENABLED_KEY] ?: true,
            intervalMinutes = (preferences[SYNC_INTERVAL_MINUTES_KEY] ?: DEFAULT_SYNC_INTERVAL_MINUTES.toInt())
                .toLong()
                .coerceAtLeast(MIN_SYNC_INTERVAL_MINUTES),
        )
    }

    suspend fun setSyncEnabled(enabled: Boolean) {
        dataStore.edit { preferences -> preferences[SYNC_ENABLED_KEY] = enabled }
    }

    suspend fun setSyncIntervalMinutes(minutes: Long) {
        dataStore.edit { preferences ->
            preferences[SYNC_INTERVAL_MINUTES_KEY] = minutes.coerceAtLeast(MIN_SYNC_INTERVAL_MINUTES).toInt()
        }
    }

    /** Validate and atomically replace the saved URL. Invalid input leaves the old value intact. */
    suspend fun replaceBaseUrl(input: String): Result<String> {
        val validated = validateBaseUrl(input).map { it.trimEnd('/') }
        validated.onSuccess { normalized ->
            dataStore.edit { preferences -> preferences[BASE_URL_KEY] = normalized }
        }
        return validated
    }

    private companion object {
        val BASE_URL_KEY = stringPreferencesKey("server_base_url")
        val SYNC_ENABLED_KEY = booleanPreferencesKey("sync_enabled")
        val SYNC_INTERVAL_MINUTES_KEY = intPreferencesKey("sync_interval_minutes")
    }
}

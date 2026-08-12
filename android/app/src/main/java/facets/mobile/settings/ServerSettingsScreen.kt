package facets.mobile.settings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.rememberCoroutineScope
import facets.mobile.data.transport.FacetsClientFactory
import facets.mobile.data.transport.FacetsException
import facets.mobile.data.transport.FacetsFailure
import facets.mobile.data.transport.InvalidServerUrlException
import facets.mobile.data.transport.ServerUrlValidator
import facets.mobile.data.model.HealthStatus
import androidx.compose.ui.unit.dp

@Composable
fun rememberServerSettingsController(): ServerSettingsController {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val store = remember(context) {
        ServerSettingsStore(context) { input ->
            runCatching { ServerUrlValidator.canonicalize(input) }
        }
    }
    return remember(store, scope) {
        ServerSettingsController(
            store = store,
            healthCheck = ::checkServerHealth,
            scope = scope,
        )
    }
}

private suspend fun checkServerHealth(baseUrl: String): ConnectionTestOutcome {
    return try {
        ServerUrlValidator.validate(baseUrl)
        val health: HealthStatus = FacetsClientFactory.create(baseUrl).checkHealth()
        if (health.healthy) ConnectionTestOutcome.Success else ConnectionTestOutcome.ServerFailure
    } catch (_: InvalidServerUrlException) {
        ConnectionTestOutcome.ValidationFailure
    } catch (error: FacetsException) {
        when (error.failure) {
            FacetsFailure.NetworkOffline -> ConnectionTestOutcome.Offline
            FacetsFailure.Timeout -> ConnectionTestOutcome.Timeout
            is FacetsFailure.InvalidUrl -> ConnectionTestOutcome.ValidationFailure
            is FacetsFailure.IncompatibleContract,
            is FacetsFailure.MalformedResponse -> ConnectionTestOutcome.Incompatible
            is FacetsFailure.InvalidRequest,
            is FacetsFailure.NotFound,
            is FacetsFailure.Conflict,
            is FacetsFailure.RemoteCancellation,
            is FacetsFailure.ServerFailure -> ConnectionTestOutcome.ServerFailure
            FacetsFailure.Cancelled -> throw kotlinx.coroutines.CancellationException()
        }
    }
}

@Composable
fun ServerSettingsScreen(
    controller: ServerSettingsController = rememberServerSettingsController(),
    modifier: Modifier = Modifier,
) {
    val state by controller.state.collectAsState()
    Column(
        modifier = modifier.fillMaxSize().padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Text("Server settings", style = MaterialTheme.typography.headlineMedium)
        Text(
            "Set the HTTPS address of your Tailnet Facets server.",
            style = MaterialTheme.typography.bodyLarge,
        )
        OutlinedTextField(
            value = state.baseUrl,
            onValueChange = controller::onUrlChanged,
            modifier = Modifier
                .fillMaxWidth()
                .semantics { contentDescription = "Server base URL" },
            label = { Text("Server URL") },
            placeholder = { Text("https://facets.example.ts.net") },
            singleLine = true,
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
            enabled = !state.isTesting && !state.isSaving,
            supportingText = {
                state.errorMessage?.let { Text(it) }
            },
            isError = state.status == ConnectionTestStatus.Validation,
        )
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Button(
                onClick = controller::saveUrl,
                enabled = !state.isSaving && !state.isTesting,
                modifier = Modifier.semantics { contentDescription = "Save server URL" },
            ) {
                if (state.isSaving) CircularProgressIndicator() else Text("Save")
            }
            Button(
                onClick = controller::testConnection,
                enabled = !state.isTesting && !state.isSaving,
                modifier = Modifier.semantics { contentDescription = "Test server connection" },
            ) {
                if (state.isTesting) CircularProgressIndicator() else Text("Test connection")
            }
        }
        ConnectionStatus(state)
    }
}

@Composable
private fun ConnectionStatus(state: ServerSettingsState) {
    val text = when (state.status) {
        ConnectionTestStatus.Idle -> ""
        ConnectionTestStatus.Testing -> "Testing connection…"
        ConnectionTestStatus.Success -> "Connection successful"
        ConnectionTestStatus.Offline -> "Server is unreachable while offline"
        ConnectionTestStatus.Timeout -> "Connection timed out"
        ConnectionTestStatus.Server -> "Server returned an error"
        ConnectionTestStatus.Validation -> state.errorMessage ?: "Enter a valid HTTPS URL"
        ConnectionTestStatus.Incompatible -> "Server API is incompatible"
    }
    if (text.isNotEmpty()) {
        Text(
            text,
            style = MaterialTheme.typography.bodyMedium,
            modifier = Modifier.semantics { contentDescription = "Connection status: $text" },
        )
    }
}

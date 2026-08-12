package facets.mobile.settings

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

/** Result of the transport's /healthz request, mapped without exposing HTTP details to Compose. */
sealed interface ConnectionTestOutcome {
    data object Success : ConnectionTestOutcome
    data object Offline : ConnectionTestOutcome
    data object Timeout : ConnectionTestOutcome
    data object ServerFailure : ConnectionTestOutcome
    data object Incompatible : ConnectionTestOutcome
    data object ValidationFailure : ConnectionTestOutcome
}

enum class ConnectionTestStatus {
    Idle,
    Testing,
    Success,
    Offline,
    Timeout,
    Server,
    Validation,
    Incompatible,
}

data class ServerSettingsState(
    val baseUrl: String = "",
    val status: ConnectionTestStatus = ConnectionTestStatus.Idle,
    val errorMessage: String? = null,
    val isSaving: Boolean = false,
) {
    val isTesting: Boolean get() = status == ConnectionTestStatus.Testing
}

/** Coordinates persistence and a transport-provided health check for the settings screen. */
class ServerSettingsController(
    private val store: ServerSettingsStore,
    private val healthCheck: suspend (String) -> ConnectionTestOutcome,
    private val scope: CoroutineScope,
) {
    private val _state = MutableStateFlow(ServerSettingsState())
    val state: StateFlow<ServerSettingsState> = _state
    val savedBaseUrl: StateFlow<String?> = store.baseUrl.stateIn(
        scope,
        SharingStarted.WhileSubscribed(5_000),
        null,
    )

    private var operation: Job? = null

    init {
        scope.launch {
            store.baseUrl.collect { url ->
                if (url != null && _state.value.baseUrl != url && !_state.value.isSaving) {
                    _state.value = _state.value.copy(baseUrl = url)
                }
            }
        }
    }

    fun onUrlChanged(value: String) {
        _state.value = _state.value.copy(
            baseUrl = value,
            status = ConnectionTestStatus.Idle,
            errorMessage = null,
        )
    }

    fun saveUrl() {
        operation?.cancel()
        operation = scope.launch {
            _state.value = _state.value.copy(isSaving = true, errorMessage = null)
            val result = store.replaceBaseUrl(_state.value.baseUrl)
            _state.value = result.fold(
                onSuccess = { normalized ->
                    _state.value.copy(
                        baseUrl = normalized,
                        status = ConnectionTestStatus.Idle,
                        errorMessage = null,
                        isSaving = false,
                    )
                },
                onFailure = { error ->
                    _state.value.copy(
                        status = ConnectionTestStatus.Validation,
                        errorMessage = error.message ?: "Enter a valid HTTPS server URL.",
                        isSaving = false,
                    )
                },
            )
        }
    }

    fun testConnection() {
        operation?.cancel()
        operation = scope.launch {
            val url = _state.value.baseUrl.trim()
            if (url.isEmpty()) {
                _state.value = _state.value.copy(
                    status = ConnectionTestStatus.Validation,
                    errorMessage = "Enter a server URL first.",
                )
                return@launch
            }
            _state.value = _state.value.copy(status = ConnectionTestStatus.Testing, errorMessage = null)
            try {
                when (healthCheck(url)) {
                    ConnectionTestOutcome.Success -> _state.value = _state.value.copy(
                        status = ConnectionTestStatus.Success,
                        errorMessage = null,
                    )
                    ConnectionTestOutcome.Offline -> updateFailure(ConnectionTestStatus.Offline)
                    ConnectionTestOutcome.Timeout -> updateFailure(ConnectionTestStatus.Timeout)
                    ConnectionTestOutcome.ServerFailure -> updateFailure(ConnectionTestStatus.Server)
                    ConnectionTestOutcome.Incompatible -> updateFailure(ConnectionTestStatus.Incompatible)
                    ConnectionTestOutcome.ValidationFailure -> updateFailure(ConnectionTestStatus.Validation)
                }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (error: Throwable) {
                updateFailure(ConnectionTestStatus.Offline, error.message)
            }
        }
    }

    private fun updateFailure(status: ConnectionTestStatus, message: String? = null) {
        _state.value = _state.value.copy(status = status, errorMessage = message)
    }
}

package forge.control.http

import io.ktor.http.HttpStatusCode
import kotlinx.serialization.Serializable

/** Platform-wide HTTP error envelope. */
@Serializable
data class ErrorEnvelope(
    val error: ErrorBody,
)

@Serializable
data class ErrorBody(
    val code: String,
    val message: String,
    val details: Map<String, String>? = null,
    val requestId: String,
)

/** Domain/API errors mapped consistently to an HTTP status and error code. */
sealed class ApiException(
    val status: HttpStatusCode,
    val code: String,
    override val message: String,
    val details: Map<String, String>? = null,
) : Exception(message) {
    class BadRequest(
        message: String,
        details: Map<String, String>? = null,
        code: String = "validation_error",
    ) : ApiException(HttpStatusCode.BadRequest, code, message, details)

    class NotFound(
        message: String,
        details: Map<String, String>? = null,
        code: String = "not_found",
    ) : ApiException(HttpStatusCode.NotFound, code, message, details)

    class Conflict(
        message: String,
        details: Map<String, String>? = null,
        code: String = "conflict",
    ) : ApiException(HttpStatusCode.Conflict, code, message, details)
}

fun ApiException.toEnvelope(): ErrorEnvelope =
    errorEnvelope(code, message, details)

fun apiError(
    code: String,
    message: String,
    details: Map<String, String>? = null,
): ErrorEnvelope = errorEnvelope(code, message, details)

fun errorEnvelope(
    code: String,
    message: String,
    details: Map<String, String>? = null,
    requestId: String = RequestId.current(),
): ErrorEnvelope = ErrorEnvelope(ErrorBody(code, message, details, requestId))

package forge.control.http

import io.ktor.http.HttpStatusCode
import kotlinx.serialization.Serializable

/** Provisional error envelope (formalized in 02.06 with requestId). */
@Serializable
data class ErrorEnvelope(
    val error: ErrorBody,
)

@Serializable
data class ErrorBody(
    val code: String,
    val message: String,
    val details: Map<String, String>? = null,
)

/** Domain/API errors mapped to HTTP status + provisional envelope. */
sealed class ApiException(
    val status: HttpStatusCode,
    val code: String,
    override val message: String,
    val details: Map<String, String>? = null,
) : Exception(message) {
    class BadRequest(
        message: String,
        details: Map<String, String>? = null,
        code: String = "invalid_request",
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
    ErrorEnvelope(ErrorBody(code = code, message = message, details = details))

fun apiError(
    code: String,
    message: String,
    details: Map<String, String>? = null,
): ErrorEnvelope = ErrorEnvelope(ErrorBody(code, message, details))

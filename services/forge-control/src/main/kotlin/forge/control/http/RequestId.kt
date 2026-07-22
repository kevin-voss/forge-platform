package forge.control.http

import io.ktor.server.application.ApplicationCall
import io.ktor.server.application.ApplicationCallPipeline
import io.ktor.server.application.Application
import io.ktor.server.application.call
import io.ktor.server.request.httpMethod
import io.ktor.util.AttributeKey
import java.util.UUID

private val requestIdKey = AttributeKey<String>("request-id")

object RequestId {
    private val fallback = ThreadLocal<String?>()

    fun current(): String = fallback.get() ?: "req_${UUID.randomUUID()}"

    fun from(call: ApplicationCall): String =
        call.attributes.getOrNull(requestIdKey) ?: current()

    internal fun set(value: String) = fallback.set(value)
    internal fun clear() = fallback.remove()
}

fun Application.installRequestId() {
    intercept(ApplicationCallPipeline.Setup) {
        val requestId = call.request.headers["X-Request-Id"]
            ?.takeIf { it.matches(Regex("""[A-Za-z0-9._-]{1,128}""")) }
            ?: "req_${UUID.randomUUID()}"
        call.attributes.put(requestIdKey, requestId)
        RequestId.set(requestId)
        call.response.headers.append("X-Request-Id", requestId)
        try {
            proceed()
        } finally {
            RequestId.clear()
        }
    }
}

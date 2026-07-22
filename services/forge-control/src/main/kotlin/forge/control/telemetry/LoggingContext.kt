package forge.control.telemetry

import forge.control.http.RequestId
import io.opentelemetry.api.trace.Span

data class LogCorrelation(
    val requestId: String,
    val traceId: String?,
    val spanId: String?,
)

/** Reads request and trace state without allowing malformed context to affect requests. */
object LoggingContext {
    fun current(): LogCorrelation {
        val spanContext = Span.current().spanContext
        return LogCorrelation(
            requestId = RequestId.current(),
            traceId = spanContext.traceId.takeIf { spanContext.isValid },
            spanId = spanContext.spanId.takeIf { spanContext.isValid },
        )
    }
}

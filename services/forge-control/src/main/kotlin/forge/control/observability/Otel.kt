package forge.control.observability

import forge.control.http.RequestId
import forge.control.telemetry.Telemetry
import io.ktor.http.Headers
import io.opentelemetry.api.trace.Span
import io.opentelemetry.api.trace.SpanKind
import io.opentelemetry.api.trace.StatusCode
import io.opentelemetry.api.trace.propagation.W3CTraceContextPropagator
import io.opentelemetry.context.Context
import io.opentelemetry.context.Scope
import io.opentelemetry.context.propagation.TextMapGetter
import io.opentelemetry.context.propagation.TextMapSetter
import java.net.http.HttpRequest

/**
 * OpenTelemetry helpers for forge-control (step 12.02 instrumentation checklist).
 *
 * SDK bootstrap lives in [Telemetry]; this type owns W3C propagation, standard
 * metric names, and outbound header injection.
 */
object Otel {
    const val METRIC_SERVICE_UP = "forge_service_up"
    const val METRIC_HTTP_REQUESTS = "forge_http_requests_total"
    const val METRIC_HTTP_DURATION = "forge_http_request_duration_seconds"

    const val HEADER_TRACEPARENT = "traceparent"
    const val HEADER_FORGE_REQUEST_ID = "X-Forge-Request-ID"
    const val HEADER_LEGACY_REQUEST_ID = "X-Request-Id"

    val FORBIDDEN_METRIC_LABELS = listOf("request_id", "trace_id", "span_id", "path", "url")

    private val propagator = W3CTraceContextPropagator.getInstance()

    private val getter = object : TextMapGetter<Headers> {
        override fun keys(carrier: Headers): Iterable<String> = carrier.names()
        override fun get(carrier: Headers?, key: String): String? = carrier?.get(key)
    }

    private val httpSetter = TextMapSetter<HttpRequest.Builder> { carrier, key, value ->
        carrier?.header(key, value)
    }

    /** Extract inbound W3C context (invalid/missing → root-capable context). */
    fun extract(headers: Headers): Context =
        propagator.extract(Context.root(), headers, getter)

    fun startServerSpan(parent: Context, method: String, path: String): Pair<Span, Scope> {
        val span = Telemetry.current().startServerSpan(parent, "HTTP $method", method, path)
        val scope = parent.with(span).makeCurrent()
        return span to scope
    }

    fun finishSpan(span: Span, scope: Scope, status: Int) {
        span.setAttribute("http.response.status_code", status.toLong())
        if (status >= 500) {
            span.setStatus(StatusCode.ERROR)
        }
        scope.close()
        span.end()
    }

    /** Inject traceparent + request ids onto an outbound Java HttpClient builder. */
    fun inject(builder: HttpRequest.Builder) {
        propagator.inject(Context.current(), builder, httpSetter)
        val requestId = RequestId.current()
        if (requestId.isNotBlank()) {
            builder.header(HEADER_FORGE_REQUEST_ID, requestId)
            builder.header(HEADER_LEGACY_REQUEST_ID, requestId)
        }
    }

    fun statusClass(status: Int): String = when {
        status >= 500 -> "5xx"
        status >= 400 -> "4xx"
        status >= 300 -> "3xx"
        status >= 200 -> "2xx"
        else -> "1xx"
    }
}

package forge.control

import forge.control.http.RequestId
import forge.control.logging.JsonLog
import forge.control.observability.Otel
import forge.control.telemetry.Telemetry
import forge.control.telemetry.TelemetryConfig
import io.ktor.http.headersOf
import io.opentelemetry.api.trace.propagation.W3CTraceContextPropagator
import io.opentelemetry.context.Context
import io.opentelemetry.context.propagation.TextMapSetter
import java.net.URI
import java.net.http.HttpRequest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertNotNull
import kotlin.test.assertTrue
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

class ObservabilityTest {
    @Test
    fun extractsInboundTraceparentAndInjectsOutbound() {
        Telemetry.initialize(TelemetryConfig(false, "forge-control", "http://127.0.0.1:1"))
        val (traceId, parentHeader) = mintTraceparent()
        val parent = Otel.extract(headersOf(Otel.HEADER_TRACEPARENT to listOf(parentHeader)))
        val (span, scope) = Otel.startServerSpan(parent, "GET", "/v1/projects")
        try {
            assertEquals(traceId, span.spanContext.traceId)
            RequestId.set("req_obs")
            val builder = HttpRequest.newBuilder().uri(URI.create("http://example/v1")).GET()
            Otel.inject(builder)
            val req = builder.build()
            val outbound = req.headers().firstValue(Otel.HEADER_TRACEPARENT).orElse("")
            assertTrue(outbound.contains(traceId), "outbound=$outbound")
            assertEquals("req_obs", req.headers().firstValue(Otel.HEADER_FORGE_REQUEST_ID).orElse(""))
        } finally {
            RequestId.clear()
            Otel.finishSpan(span, scope, 200)
        }
    }

    @Test
    fun malformedTraceparentStartsNewRoot() {
        Telemetry.initialize(TelemetryConfig(false, "forge-control", "http://127.0.0.1:1"))
        val parent = Otel.extract(headersOf(Otel.HEADER_TRACEPARENT to listOf("not-valid")))
        val (span, scope) = Otel.startServerSpan(parent, "GET", "/v1/x")
        try {
            assertTrue(span.spanContext.isValid)
            assertNotEquals("00000000000000000000000000000000", span.spanContext.traceId)
        } finally {
            Otel.finishSpan(span, scope, 200)
        }
    }

    @Test
    fun logEnricherAddsSnakeCaseCorrelationFields() {
        Telemetry.initialize(TelemetryConfig(false, "forge-control", "http://127.0.0.1:1"))
        val parent = Otel.extract(headersOf())
        val (span, scope) = Otel.startServerSpan(parent, "GET", "/v1/x")
        val original = System.out
        val output = java.io.ByteArrayOutputStream()
        try {
            System.setOut(java.io.PrintStream(output))
            RequestId.set("req_corr")
            JsonLog("forge-control", "info").info("correlated")
        } finally {
            RequestId.clear()
            System.setOut(original)
            Otel.finishSpan(span, scope, 200)
        }
        val log = Json.parseToJsonElement(output.toString().trim()).jsonObject
        assertEquals("req_corr", log["request_id"]?.jsonPrimitive?.content)
        assertEquals("req_corr", log["requestId"]?.jsonPrimitive?.content)
        assertNotNull(log["trace_id"]?.jsonPrimitive?.content)
        assertNotNull(log["span_id"]?.jsonPrimitive?.content)
        assertEquals("forge-control", log["forge.service"]?.jsonPrimitive?.content)
    }

    @Test
    fun metricLabelsForbidHighCardinalityKeys() {
        assertTrue(Otel.FORBIDDEN_METRIC_LABELS.contains("request_id"))
        assertTrue(Otel.FORBIDDEN_METRIC_LABELS.contains("trace_id"))
        assertEquals("forge_http_requests_total", Otel.METRIC_HTTP_REQUESTS)
        assertEquals("forge_http_request_duration_seconds", Otel.METRIC_HTTP_DURATION)
        assertEquals("forge_service_up", Otel.METRIC_SERVICE_UP)
    }

    @Test
    fun failOpenInitAgainstUnreachableCollector() {
        val telemetry = Telemetry.initialize(
            TelemetryConfig(
                enabled = true,
                serviceName = "forge-control",
                otlpEndpoint = "http://127.0.0.1:1",
                environment = "test",
            ),
        )
        // Init must not throw; recording must not throw.
        assertTrue(telemetry.enabled)
        telemetry.recordRequest("GET", 200, 12)
        telemetry.close()
    }

    private fun mintTraceparent(): Pair<String, String> {
        Telemetry.initialize(TelemetryConfig(false, "forge-control", "http://127.0.0.1:1"))
        val span = Telemetry.current().startSpan("mint")
        val scope = span.makeCurrent()
        val carrier = mutableMapOf<String, String>()
        val setter = TextMapSetter<MutableMap<String, String>> { c, k, v -> c?.put(k, v) }
        try {
            W3CTraceContextPropagator.getInstance().inject(Context.current(), carrier, setter)
        } finally {
            scope.close()
            span.end()
        }
        val header = carrier["traceparent"] ?: error("missing traceparent")
        val traceId = header.split("-")[1]
        assertFalse(traceId.all { it == '0' })
        return traceId to header
    }
}

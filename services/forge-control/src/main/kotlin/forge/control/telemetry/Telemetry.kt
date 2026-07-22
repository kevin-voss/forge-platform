package forge.control.telemetry

import io.opentelemetry.api.OpenTelemetry
import io.opentelemetry.api.common.AttributeKey
import io.opentelemetry.api.common.Attributes
import io.opentelemetry.api.metrics.LongCounter
import io.opentelemetry.api.metrics.DoubleHistogram
import io.opentelemetry.api.trace.Span
import io.opentelemetry.api.trace.StatusCode
import io.opentelemetry.api.trace.Tracer
import io.opentelemetry.context.Scope
import io.opentelemetry.exporter.otlp.metrics.OtlpGrpcMetricExporter
import io.opentelemetry.exporter.otlp.trace.OtlpGrpcSpanExporter
import io.opentelemetry.sdk.OpenTelemetrySdk
import io.opentelemetry.sdk.metrics.SdkMeterProvider
import io.opentelemetry.sdk.metrics.export.PeriodicMetricReader
import io.opentelemetry.sdk.resources.Resource
import io.opentelemetry.sdk.trace.SdkTracerProvider
import io.opentelemetry.sdk.trace.export.BatchSpanProcessor
import java.util.concurrent.TimeUnit

data class TelemetryConfig(
    val enabled: Boolean,
    val serviceName: String,
    val otlpEndpoint: String,
)

/**
 * Process-local telemetry facade. Export failures are handled by the OTEL SDK and
 * never enter the request path.
 */
class Telemetry private constructor(
    private val tracer: Tracer,
    private val requestCount: LongCounter,
    private val requestDuration: DoubleHistogram,
    private val errorCount: LongCounter,
    private val sdk: OpenTelemetrySdk?,
) : AutoCloseable {
    val enabled: Boolean = sdk != null

    fun startSpan(name: String): Span = tracer.spanBuilder(name).startSpan()

    fun recordRequest(status: Int, durationMs: Long) {
        val attributes = Attributes.of(AttributeKey.longKey("http.response.status_code"), status.toLong())
        requestCount.add(1, attributes)
        requestDuration.record(durationMs.toDouble(), attributes)
        if (status >= 400) errorCount.add(1, attributes)
    }

    fun <T> inSpan(name: String, block: () -> T): T {
        val span = startSpan(name)
        val scope: Scope = span.makeCurrent()
        return try {
            block()
        } catch (error: Throwable) {
            span.recordException(error)
            span.setStatus(StatusCode.ERROR)
            throw error
        } finally {
            scope.close()
            span.end()
        }
    }

    override fun close() {
        sdk?.shutdown()?.join(5, TimeUnit.SECONDS)
    }

    companion object {
        @Volatile
        private var active: Telemetry = disabled()

        fun current(): Telemetry = active

        fun initialize(config: TelemetryConfig): Telemetry {
            active.close()
            active = if (config.enabled) create(config) else disabled()
            return active
        }

        private fun create(config: TelemetryConfig): Telemetry {
            val resource = Resource.getDefault().merge(
                Resource.create(
                    Attributes.of(AttributeKey.stringKey("service.name"), config.serviceName),
                ),
            )
            val tracerProvider = SdkTracerProvider.builder()
                .setResource(resource)
                .addSpanProcessor(
                    BatchSpanProcessor.builder(
                        OtlpGrpcSpanExporter.builder().setEndpoint(config.otlpEndpoint).build(),
                    ).build(),
                )
                .build()
            val meterProvider = SdkMeterProvider.builder()
                .setResource(resource)
                .registerMetricReader(
                    PeriodicMetricReader.builder(
                        OtlpGrpcMetricExporter.builder().setEndpoint(config.otlpEndpoint).build(),
                    ).build(),
                )
                .build()
            val sdk = OpenTelemetrySdk.builder()
                .setTracerProvider(tracerProvider)
                .setMeterProvider(meterProvider)
                .build()
            return fromOpenTelemetry(sdk, sdk)
        }

        private fun disabled(): Telemetry = fromOpenTelemetry(OpenTelemetry.noop(), null)

        private fun fromOpenTelemetry(openTelemetry: OpenTelemetry, sdk: OpenTelemetrySdk?): Telemetry {
            val meter = openTelemetry.getMeter("forge.control")
            return Telemetry(
                tracer = openTelemetry.getTracer("forge.control"),
                requestCount = meter.counterBuilder("http.server.requests").build(),
                requestDuration = meter.histogramBuilder("http.server.duration").setUnit("ms").build(),
                errorCount = meter.counterBuilder("http.server.errors").build(),
                sdk = sdk,
            )
        }
    }
}

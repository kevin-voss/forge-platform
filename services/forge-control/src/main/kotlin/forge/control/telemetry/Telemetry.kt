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
    private val reconcileTicks: LongCounter,
    private val reconcilePlanActions: LongCounter,
    private val reconcileActions: LongCounter,
    private val replicasReady: LongCounter,
    private val rolloutSteps: LongCounter,
    private val rolloutResults: LongCounter,
    private val rollbackDuration: DoubleHistogram,
    private val deploymentTransitions: LongCounter,
    private val placements: LongCounter,
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

    fun recordPlacement(strategy: String) {
        placements.add(
            1,
            Attributes.of(AttributeKey.stringKey("strategy"), strategy),
        )
    }

    fun recordReconcileTick(planActions: Int, healthy: Boolean) {
        val attributes = Attributes.of(
            AttributeKey.booleanKey("controller.healthy"),
            healthy,
        )
        reconcileTicks.add(1, attributes)
        reconcilePlanActions.add(planActions.toLong(), attributes)
    }

    fun recordReconcileAction(action: String) {
        reconcileActions.add(
            1,
            Attributes.of(AttributeKey.stringKey("action"), action),
        )
    }

    fun recordReplicasReady(count: Int) {
        replicasReady.add(count.toLong())
    }

    fun recordRolloutStep(step: String) {
        rolloutSteps.add(
            1,
            Attributes.of(AttributeKey.stringKey("step"), step),
        )
    }

    fun recordRolloutResult(result: String) {
        rolloutResults.add(
            1,
            Attributes.of(AttributeKey.stringKey("result"), result),
        )
    }

    fun recordRollbackDuration(durationMs: Long) {
        rollbackDuration.record(durationMs.toDouble())
    }

    fun recordDeploymentTransition(toStatus: String) {
        deploymentTransitions.add(
            1,
            Attributes.of(AttributeKey.stringKey("to_status"), toStatus),
        )
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
                reconcileTicks = meter.counterBuilder("forge_reconcile_ticks_total").build(),
                reconcilePlanActions = meter.counterBuilder("forge_reconcile_plan_actions").build(),
                reconcileActions = meter.counterBuilder("forge_reconcile_actions_total").build(),
                replicasReady = meter.counterBuilder("forge_replicas_ready").build(),
                rolloutSteps = meter.counterBuilder("forge_rollout_step_total").build(),
                rolloutResults = meter.counterBuilder("forge_rollout_result_total").build(),
                rollbackDuration = meter.histogramBuilder("forge_rollback_duration_ms").setUnit("ms").build(),
                deploymentTransitions = meter.counterBuilder("forge_deployment_transitions_total").build(),
                placements = meter.counterBuilder("forge_placements_total").build(),
                sdk = sdk,
            )
        }
    }
}

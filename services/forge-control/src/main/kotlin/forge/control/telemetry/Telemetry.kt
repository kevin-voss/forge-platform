package forge.control.telemetry

import forge.control.observability.Otel
import io.opentelemetry.api.OpenTelemetry
import io.opentelemetry.api.common.AttributeKey
import io.opentelemetry.api.common.Attributes
import io.opentelemetry.api.metrics.LongCounter
import io.opentelemetry.api.metrics.DoubleHistogram
import io.opentelemetry.api.metrics.ObservableLongGauge
import io.opentelemetry.api.trace.Span
import io.opentelemetry.api.trace.SpanKind
import io.opentelemetry.api.trace.StatusCode
import io.opentelemetry.api.trace.Tracer
import io.opentelemetry.context.Context
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
import java.util.concurrent.atomic.AtomicLong

data class TelemetryConfig(
    val enabled: Boolean,
    val serviceName: String,
    val otlpEndpoint: String,
    val environment: String = "development",
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
    private val forgeHttpRequests: LongCounter,
    private val forgeHttpDuration: DoubleHistogram,
    @Suppress("unused") private val forgeServiceUp: ObservableLongGauge,
    private val reconcileTicks: LongCounter,
    private val reconcilePlanActions: LongCounter,
    private val reconcileActions: LongCounter,
    private val replicasReady: LongCounter,
    private val rolloutSteps: LongCounter,
    private val rolloutResults: LongCounter,
    private val rollbackDuration: DoubleHistogram,
    private val deploymentTransitions: LongCounter,
    private val placements: LongCounter,
    private val placementDecisions: LongCounter,
    private val placementRejectedNoCapacity: LongCounter,
    private val antiAffinityFallback: LongCounter,
    private val queueDrain: LongCounter,
    private val placementsPendingValue: AtomicLong,
    @Suppress("unused") private val placementsPendingGauge: ObservableLongGauge,
    private val nodesTotal: LongCounter,
    private val nodeFreeSlots: LongCounter,
    private val nodeHeartbeatAge: DoubleHistogram,
    private val rescheduleTotal: LongCounter,
    private val nodeOfflineTotal: LongCounter,
    private val staleReplicasFenced: LongCounter,
    private val managedDbInstances: LongCounter,
    private val managedDbProvisionDuration: DoubleHistogram,
    private val managedDbProvisionErrors: LongCounter,
    private val managedDbAttachments: LongCounter,
    private val managedDbBackups: LongCounter,
    private val managedDbRestores: LongCounter,
    private val managedDbRotations: LongCounter,
    private val managedDbDeletes: LongCounter,
    private val resourceWrites: LongCounter,
    private val resourceGenerationBumps: LongCounter,
    private val resourceConditionTransitions: LongCounter,
    private val resourceStatusWrites: LongCounter,
    private val resourceListRequests: LongCounter,
    private val resourceListPageSize: DoubleHistogram,
    private val resourceEventsEmitted: LongCounter,
    private val resourceTerminating: LongCounter,
    private val resourcesByKind: java.util.concurrent.ConcurrentHashMap<String, AtomicLong>,
    @Suppress("unused") private val resourcesGauge: ObservableLongGauge,
    private val watchConnectionsByKind: java.util.concurrent.ConcurrentHashMap<String, AtomicLong>,
    @Suppress("unused") private val watchConnectionsGauge: ObservableLongGauge,
    private val sdk: OpenTelemetrySdk?,
) : AutoCloseable {
    val enabled: Boolean = sdk != null

    fun startSpan(name: String): Span = tracer.spanBuilder(name).startSpan()

    fun recordResourceWrite(kind: String, action: String) {
        resourceWrites.add(
            1,
            Attributes.of(
                AttributeKey.stringKey("kind"),
                kind,
                AttributeKey.stringKey("action"),
                action,
            ),
        )
        if (action == "create") {
            resourcesByKind.computeIfAbsent(kind) { AtomicLong(0) }.incrementAndGet()
        } else if (action == "delete") {
            resourcesByKind.computeIfAbsent(kind) { AtomicLong(0) }.updateAndGet { (it - 1).coerceAtLeast(0) }
        }
    }

    fun recordResourceGenerationBump(kind: String) {
        resourceGenerationBumps.add(
            1,
            Attributes.of(AttributeKey.stringKey("kind"), kind),
        )
    }

    fun recordResourceConditionTransition(kind: String, type: String, status: String) {
        resourceConditionTransitions.add(
            1,
            Attributes.of(
                AttributeKey.stringKey("kind"),
                kind,
                AttributeKey.stringKey("type"),
                type,
                AttributeKey.stringKey("status"),
                status,
            ),
        )
    }

    fun recordResourceStatusWrite(kind: String) {
        resourceStatusWrites.add(
            1,
            Attributes.of(AttributeKey.stringKey("kind"), kind),
        )
    }

    fun recordResourceList(kind: String, pageSize: Int) {
        resourceListRequests.add(
            1,
            Attributes.of(AttributeKey.stringKey("kind"), kind),
        )
        resourceListPageSize.record(
            pageSize.toDouble(),
            Attributes.of(AttributeKey.stringKey("kind"), kind),
        )
    }

    fun recordResourceEventEmitted(kind: String, type: String) {
        resourceEventsEmitted.add(
            1,
            Attributes.of(
                AttributeKey.stringKey("kind"),
                kind,
                AttributeKey.stringKey("type"),
                type,
            ),
        )
    }

    fun recordResourceTerminating(kind: String) {
        resourceTerminating.add(
            1,
            Attributes.of(AttributeKey.stringKey("kind"), kind),
        )
    }

    fun watchConnectionOpened(kind: String) {
        watchConnectionsByKind.computeIfAbsent(kind) { AtomicLong(0) }.incrementAndGet()
    }

    fun watchConnectionClosed(kind: String) {
        watchConnectionsByKind.computeIfAbsent(kind) { AtomicLong(0) }.updateAndGet { (it - 1).coerceAtLeast(0) }
    }

    fun startServerSpan(parent: Context, name: String, method: String, path: String): Span =
        tracer.spanBuilder(name)
            .setParent(parent)
            .setSpanKind(SpanKind.SERVER)
            .setAttribute("http.request.method", method)
            .setAttribute("url.path", path)
            .setAttribute("forge.service", "forge-control")
            .startSpan()

    fun recordRequest(status: Int, durationMs: Long) {
        recordRequest("HTTP", status, durationMs)
    }

    fun recordRequest(method: String, status: Int, durationMs: Long) {
        val attributes = Attributes.of(AttributeKey.longKey("http.response.status_code"), status.toLong())
        requestCount.add(1, attributes)
        requestDuration.record(durationMs.toDouble(), attributes)
        if (status >= 400) errorCount.add(1, attributes)
        val forgeAttrs = Attributes.of(
            AttributeKey.stringKey("http_method"), method,
            AttributeKey.stringKey("http_status_class"), Otel.statusClass(status),
        )
        forgeHttpRequests.add(1, forgeAttrs)
        forgeHttpDuration.record(durationMs / 1000.0, forgeAttrs)
    }

    fun recordPlacement(strategy: String) {
        placements.add(
            1,
            Attributes.of(AttributeKey.stringKey("strategy"), strategy),
        )
    }

    fun recordPlacementDecision(strategy: String, node: String) {
        placementDecisions.add(
            1,
            Attributes.of(
                AttributeKey.stringKey("strategy"),
                strategy,
                AttributeKey.stringKey("node"),
                node,
            ),
        )
    }

    fun recordPlacementRejectedNoCapacity() {
        placementRejectedNoCapacity.add(1)
    }

    fun recordAntiAffinityFallback() {
        antiAffinityFallback.add(1)
    }

    fun recordQueueDrain() {
        queueDrain.add(1)
    }

    fun setPlacementsPending(count: Int) {
        placementsPendingValue.set(count.toLong().coerceAtLeast(0))
    }

    fun recordNodeStatus(status: String) {
        nodesTotal.add(
            1,
            Attributes.of(AttributeKey.stringKey("status"), status),
        )
    }

    fun recordNodeFreeSlots(nodeId: String, freeSlots: Int) {
        nodeFreeSlots.add(
            freeSlots.toLong(),
            Attributes.of(AttributeKey.stringKey("node"), nodeId),
        )
    }

    fun recordNodeHeartbeatAge(nodeId: String, ageSeconds: Long) {
        nodeHeartbeatAge.record(
            ageSeconds.toDouble(),
            Attributes.of(AttributeKey.stringKey("node"), nodeId),
        )
    }

    fun recordReschedule(result: String) {
        rescheduleTotal.add(
            1,
            Attributes.of(AttributeKey.stringKey("result"), result),
        )
    }

    fun recordNodeOffline() {
        nodeOfflineTotal.add(1)
    }

    fun recordStaleReplicaFenced() {
        staleReplicasFenced.add(1)
    }

    fun recordManagedDbInstance(status: String) {
        managedDbInstances.add(
            1,
            Attributes.of(AttributeKey.stringKey("status"), status),
        )
    }

    fun recordManagedDbProvisionDuration(seconds: Double, op: String) {
        managedDbProvisionDuration.record(
            seconds,
            Attributes.of(AttributeKey.stringKey("op"), op),
        )
    }

    fun recordManagedDbProvisionError(op: String) {
        managedDbProvisionErrors.add(
            1,
            Attributes.of(AttributeKey.stringKey("op"), op),
        )
    }

    fun recordManagedDbAttachment() {
        managedDbAttachments.add(1)
    }

    fun recordManagedDbBackup(status: String) {
        managedDbBackups.add(
            1,
            Attributes.of(AttributeKey.stringKey("status"), status),
        )
    }

    fun recordManagedDbRestore(status: String) {
        managedDbRestores.add(
            1,
            Attributes.of(AttributeKey.stringKey("status"), status),
        )
    }

    fun recordManagedDbRotation(status: String) {
        managedDbRotations.add(
            1,
            Attributes.of(AttributeKey.stringKey("status"), status),
        )
    }

    fun recordManagedDbDelete(forced: Boolean) {
        managedDbDeletes.add(
            1,
            Attributes.of(AttributeKey.stringKey("forced"), forced.toString()),
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
                    Attributes.builder()
                        .put(AttributeKey.stringKey("service.name"), config.serviceName)
                        .put(AttributeKey.stringKey("deployment.environment"), config.environment)
                        .put(AttributeKey.stringKey("forge.service"), config.serviceName)
                        .build(),
                ),
            )
            val tracerProvider = SdkTracerProvider.builder()
                .setResource(resource)
                .addSpanProcessor(
                    BatchSpanProcessor.builder(
                        OtlpGrpcSpanExporter.builder()
                            .setEndpoint(config.otlpEndpoint)
                            .setTimeout(2, TimeUnit.SECONDS)
                            .build(),
                    )
                        .setScheduleDelay(2, TimeUnit.SECONDS)
                        .setExporterTimeout(2, TimeUnit.SECONDS)
                        .build(),
                )
                .build()
            val meterProvider = SdkMeterProvider.builder()
                .setResource(resource)
                .registerMetricReader(
                    PeriodicMetricReader.builder(
                        OtlpGrpcMetricExporter.builder()
                            .setEndpoint(config.otlpEndpoint)
                            .setTimeout(2, TimeUnit.SECONDS)
                            .build(),
                    )
                        .setInterval(15, TimeUnit.SECONDS)
                        .build(),
                )
                .build()
            val sdk = OpenTelemetrySdk.builder()
                .setTracerProvider(tracerProvider)
                .setMeterProvider(meterProvider)
                .build()
            return fromOpenTelemetry(sdk, sdk)
        }

        /**
         * No OTLP export, but a real in-process TracerProvider so W3C propagation
         * and log correlation still produce valid trace/span ids (fail-open / tests).
         */
        private fun disabled(): Telemetry {
            val resource = Resource.getDefault().merge(
                Resource.create(
                    Attributes.of(AttributeKey.stringKey("service.name"), "forge-control"),
                ),
            )
            val tracerProvider = SdkTracerProvider.builder().setResource(resource).build()
            val meterProvider = SdkMeterProvider.builder().setResource(resource).build()
            val sdk = OpenTelemetrySdk.builder()
                .setTracerProvider(tracerProvider)
                .setMeterProvider(meterProvider)
                .build()
            // Pass sdk=null so [enabled] stays false (no remote export).
            return fromOpenTelemetry(sdk, null)
        }

        private fun fromOpenTelemetry(openTelemetry: OpenTelemetry, sdk: OpenTelemetrySdk?): Telemetry {
            val meter = openTelemetry.getMeter("forge.control")
            val pendingValue = AtomicLong(0)
            val pendingGauge = meter.gaugeBuilder("forge_placements_pending")
                .ofLongs()
                .buildWithCallback { measurement ->
                    measurement.record(pendingValue.get())
                }
            val upValue = AtomicLong(1)
            val serviceUp = meter.gaugeBuilder(Otel.METRIC_SERVICE_UP)
                .ofLongs()
                .buildWithCallback { measurement ->
                    measurement.record(if (sdk != null) upValue.get() else 0)
                }
            val resourceCounts = java.util.concurrent.ConcurrentHashMap<String, AtomicLong>()
            val watchCounts = java.util.concurrent.ConcurrentHashMap<String, AtomicLong>()
            return Telemetry(
                tracer = openTelemetry.getTracer("forge.control"),
                requestCount = meter.counterBuilder("http.server.requests").build(),
                requestDuration = meter.histogramBuilder("http.server.duration").setUnit("ms").build(),
                errorCount = meter.counterBuilder("http.server.errors").build(),
                forgeHttpRequests = meter.counterBuilder(Otel.METRIC_HTTP_REQUESTS).build(),
                forgeHttpDuration = meter.histogramBuilder(Otel.METRIC_HTTP_DURATION)
                    .setUnit("s")
                    .build(),
                forgeServiceUp = serviceUp,
                reconcileTicks = meter.counterBuilder("forge_reconcile_ticks_total").build(),
                reconcilePlanActions = meter.counterBuilder("forge_reconcile_plan_actions").build(),
                reconcileActions = meter.counterBuilder("forge_reconcile_actions_total").build(),
                replicasReady = meter.counterBuilder("forge_replicas_ready").build(),
                rolloutSteps = meter.counterBuilder("forge_rollout_step_total").build(),
                rolloutResults = meter.counterBuilder("forge_rollout_result_total").build(),
                rollbackDuration = meter.histogramBuilder("forge_rollback_duration_ms").setUnit("ms").build(),
                deploymentTransitions = meter.counterBuilder("forge_deployment_transitions_total").build(),
                placements = meter.counterBuilder("forge_placements_total").build(),
                placementDecisions = meter.counterBuilder("forge_placement_decisions_total").build(),
                placementRejectedNoCapacity = meter
                    .counterBuilder("forge_placement_rejected_no_capacity_total")
                    .build(),
                antiAffinityFallback = meter
                    .counterBuilder("forge_anti_affinity_fallback_total")
                    .build(),
                queueDrain = meter.counterBuilder("forge_queue_drain_total").build(),
                placementsPendingValue = pendingValue,
                placementsPendingGauge = pendingGauge,
                nodesTotal = meter.counterBuilder("forge_nodes_total").build(),
                nodeFreeSlots = meter.counterBuilder("forge_node_free_slots").build(),
                nodeHeartbeatAge = meter.histogramBuilder("forge_node_heartbeat_age_seconds")
                    .setUnit("s")
                    .build(),
                rescheduleTotal = meter.counterBuilder("forge_reschedule_total").build(),
                nodeOfflineTotal = meter.counterBuilder("forge_node_offline_total").build(),
                staleReplicasFenced = meter
                    .counterBuilder("forge_stale_replicas_fenced_total")
                    .build(),
                managedDbInstances = meter.counterBuilder("managed_db_instances_total").build(),
                managedDbProvisionDuration = meter
                    .histogramBuilder("managed_db_provision_duration_seconds")
                    .setUnit("s")
                    .build(),
                managedDbProvisionErrors = meter
                    .counterBuilder("managed_db_provision_errors_total")
                    .build(),
                managedDbAttachments = meter
                    .counterBuilder("managed_db_attachments_total")
                    .build(),
                managedDbBackups = meter
                    .counterBuilder("managed_db_backups_total")
                    .build(),
                managedDbRestores = meter
                    .counterBuilder("managed_db_restore_total")
                    .build(),
                managedDbRotations = meter
                    .counterBuilder("managed_db_rotations_total")
                    .build(),
                managedDbDeletes = meter
                    .counterBuilder("managed_db_deletes_total")
                    .build(),
                resourceWrites = meter
                    .counterBuilder("forge_resource_writes_total")
                    .build(),
                resourceGenerationBumps = meter
                    .counterBuilder("forge_resource_generation_bumps_total")
                    .build(),
                resourceConditionTransitions = meter
                    .counterBuilder("forge_resource_condition_transitions_total")
                    .build(),
                resourceStatusWrites = meter
                    .counterBuilder("forge_resource_status_writes_total")
                    .build(),
                resourceListRequests = meter
                    .counterBuilder("forge_resource_list_requests_total")
                    .build(),
                resourceListPageSize = meter
                    .histogramBuilder("forge_resource_list_page_size")
                    .build(),
                resourceEventsEmitted = meter
                    .counterBuilder("forge_resource_events_emitted_total")
                    .build(),
                resourceTerminating = meter
                    .counterBuilder("forge_resource_terminating_total")
                    .build(),
                resourcesByKind = resourceCounts,
                resourcesGauge = meter.gaugeBuilder("forge_resources_total")
                    .ofLongs()
                    .buildWithCallback { measurement ->
                        for ((kind, count) in resourceCounts) {
                            measurement.record(
                                count.get(),
                                Attributes.of(AttributeKey.stringKey("kind"), kind),
                            )
                        }
                    },
                watchConnectionsByKind = watchCounts,
                watchConnectionsGauge = meter.gaugeBuilder("forge_resource_watch_connections")
                    .ofLongs()
                    .buildWithCallback { measurement ->
                        for ((kind, count) in watchCounts) {
                            measurement.record(
                                count.get(),
                                Attributes.of(AttributeKey.stringKey("kind"), kind),
                            )
                        }
                    },
                sdk = sdk,
            )
        }
    }
}

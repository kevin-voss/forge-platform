package forge.control.scheduler

import forge.control.logging.JsonLog
import forge.control.scheduler.model.AntiAffinity
import forge.control.scheduler.model.PlacementDecision
import forge.control.scheduler.model.PlacementRequest
import forge.control.scheduler.model.ResourceRequirements
import forge.control.telemetry.Telemetry
import io.opentelemetry.api.common.AttributeKey
import io.opentelemetry.api.trace.Span
import java.time.Instant
import java.util.UUID

/**
 * Orchestrates place + persist for the placement API and reconciler.
 * Idempotent for (deployment, replica_index).
 */
class PlacementService(
    private val scheduler: Scheduler,
    private val store: PlacementStore,
    private val log: JsonLog,
    private val telemetry: Telemetry = Telemetry.current(),
    private val reservation: CapacityReservation? = null,
    private val idFactory: () -> String = { "plc_${UUID.randomUUID().toString().replace("-", "").take(12)}" },
    private val clock: () -> Instant = { Instant.now() },
) {
    fun placeAndPersist(
        deploymentId: UUID,
        replicaIndex: Int,
        serviceId: String? = null,
        slots: Int = 1,
        antiAffinity: AntiAffinity = AntiAffinity.Soft,
    ): PlaceResult {
        store.find(deploymentId, replicaIndex)?.let { existing ->
            return PlaceResult.Ok(existing, created = false)
        }

        val requirements = ResourceRequirements(slots = slots)
        val request = PlacementRequest(
            deploymentId = deploymentId.toString(),
            replicaIndex = replicaIndex,
            serviceId = serviceId,
            requirements = requirements,
            antiAffinity = antiAffinity,
        )
        val decision = telemetry.inSpan("scheduler.place") {
            val result = scheduler.place(request)
            val span = Span.current()
            when (result) {
                is PlacementDecision.Assigned -> {
                    span.setAttribute(AttributeKey.stringKey("strategy"), result.strategy)
                    span.setAttribute(AttributeKey.stringKey("node"), result.nodeId)
                    span.setAttribute(AttributeKey.longKey("candidates"), 1)
                }
                is PlacementDecision.NoNodeAvailable -> {
                    span.setAttribute(AttributeKey.stringKey("strategy"), "none")
                    span.setAttribute(AttributeKey.longKey("candidates"), 0)
                }
            }
            result
        }
        return when (decision) {
            is PlacementDecision.NoNodeAvailable -> {
                telemetry.recordPlacementRejectedNoCapacity()
                log.info(
                    "placement rejected no capacity",
                    "deployment_id" to deploymentId.toString(),
                    "replica_index" to replicaIndex,
                    "reason" to decision.reason,
                )
                PlaceResult.NoNode(decision.reason)
            }
            is PlacementDecision.Assigned -> {
                val placement = store.upsert(
                    Placement(
                        id = idFactory(),
                        deploymentId = deploymentId,
                        replicaIndex = replicaIndex,
                        nodeId = decision.nodeId,
                        strategy = decision.strategy,
                        reason = decision.reason,
                        createdAt = clock(),
                    ),
                )
                log.info(
                    "placement recorded",
                    "deployment_id" to deploymentId.toString(),
                    "replica_index" to replicaIndex,
                    "node_id" to placement.nodeId,
                    "strategy" to placement.strategy,
                    "reason" to (placement.reason ?: ""),
                    "placement_id" to placement.id,
                )
                telemetry.recordPlacement(placement.strategy)
                telemetry.recordPlacementDecision(placement.strategy, placement.nodeId)
                PlaceResult.Ok(placement, created = true)
            }
        }
    }

    fun list(deploymentId: UUID): List<Placement> = store.listByDeployment(deploymentId)

    /**
     * Delete placement and release reserved capacity. Hook for stop/reschedule (08.05).
     */
    fun releasePlacement(
        deploymentId: UUID,
        replicaIndex: Int,
        slots: Int = 1,
    ): Placement? {
        val deleted = store.delete(deploymentId, replicaIndex) ?: return null
        reservation?.releaseSlots(deleted.nodeId, slots)
        log.info(
            "placement released",
            "deployment_id" to deploymentId.toString(),
            "replica_index" to replicaIndex,
            "node_id" to deleted.nodeId,
            "placement_id" to deleted.id,
        )
        return deleted
    }
}

sealed class PlaceResult {
    data class Ok(val placement: Placement, val created: Boolean) : PlaceResult()
    data class NoNode(val reason: String) : PlaceResult()
}

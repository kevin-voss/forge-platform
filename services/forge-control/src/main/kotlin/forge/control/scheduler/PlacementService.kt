package forge.control.scheduler

import forge.control.logging.JsonLog
import forge.control.scheduler.model.AntiAffinity
import forge.control.scheduler.model.PlacementDecision
import forge.control.scheduler.model.PlacementRequest
import forge.control.scheduler.model.ResourceRequirements
import forge.control.telemetry.Telemetry
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

        val request = PlacementRequest(
            deploymentId = deploymentId.toString(),
            replicaIndex = replicaIndex,
            serviceId = serviceId,
            requirements = ResourceRequirements(slots = slots),
            antiAffinity = antiAffinity,
        )
        val decision = telemetry.inSpan("scheduler.place") {
            scheduler.place(request)
        }
        return when (decision) {
            is PlacementDecision.NoNodeAvailable ->
                PlaceResult.NoNode(decision.reason)
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
                PlaceResult.Ok(placement, created = true)
            }
        }
    }

    fun list(deploymentId: UUID): List<Placement> = store.listByDeployment(deploymentId)
}

sealed class PlaceResult {
    data class Ok(val placement: Placement, val created: Boolean) : PlaceResult()
    data class NoNode(val reason: String) : PlaceResult()
}

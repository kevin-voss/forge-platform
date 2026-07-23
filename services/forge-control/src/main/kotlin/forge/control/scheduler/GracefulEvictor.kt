package forge.control.scheduler

import forge.control.logging.JsonLog
import forge.control.scheduler.model.ResourceRequirements
import forge.control.telemetry.Telemetry
import java.time.Duration

/**
 * Evicts preemption victims: mark lost, release capacity immediately, then
 * re-submit as a fresh placement request (reschedule-or-pending).
 *
 * [grace] is recorded for Runtime stop-with-grace and does not block the
 * scheduler hot path (capacity must free immediately for the preemptor).
 */
class GracefulEvictor(
    private val store: PlacementStore,
    private val reservation: CapacityReservation,
    private val log: JsonLog,
    private val grace: Duration = Duration.ofSeconds(10),
    private val telemetry: Telemetry = Telemetry.current(),
) {
    /** Wired after [PlacementService] construction to avoid a cycle. */
    var resubmitFn: ((Placement) -> PlaceResult)? = null

    /**
     * Mark [victim] lost and free its reserved capacity. Does not reschedule yet
     * so the preemptor can claim the freed capacity first.
     */
    fun releaseVictim(victim: Placement, preemptedByPlacementId: String?): Placement? {
        val lost = store.markLost(victim.deploymentId, victim.replicaIndex) ?: return null
        if (preemptedByPlacementId != null) {
            store.markPreemptedBy(lost.id, preemptedByPlacementId)
        }
        val nodeId = lost.nodeId
        if (!nodeId.isNullOrBlank()) {
            val releaseReqs = when {
                lost.requests != null && !lost.requests.isEmpty() ->
                    RequirementsResolver.resolve(
                        ResourceRequirements(
                            slots = lost.slots.coerceAtLeast(1),
                            requests = lost.requests,
                            limits = lost.limits,
                            slotsExplicit = true,
                        ),
                    ).toResourceRequirements()
                else -> ResourceRequirements(slots = lost.slots.coerceAtLeast(1))
            }
            reservation.release(nodeId, releaseReqs)
        }
        log.info(
            "preemption victim released",
            "event" to "preemption",
            "victim_placement" to lost.id,
            "preemptor_placement" to (preemptedByPlacementId ?: ""),
            "node" to (nodeId ?: ""),
            "grace_s" to grace.seconds,
        )
        return lost
    }

    /** Re-submit a previously released victim as a fresh placement request. */
    fun resubmitVictim(lost: Placement): PlaceResult {
        val fn = resubmitFn
            ?: return PlaceResult.NoNode("preemption resubmit not wired")
        val result = fn(lost)
        telemetry.recordReschedule(
            when (result) {
                is PlaceResult.Ok -> "placed"
                is PlaceResult.Pending -> "pending"
                is PlaceResult.NoNode -> "pending"
                is PlaceResult.QueueFull -> "pending"
            },
        )
        return result
    }
}

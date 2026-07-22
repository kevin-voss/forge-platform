package forge.control.scheduler

import forge.control.scheduler.model.PlacementDecision
import forge.control.scheduler.model.PlacementRequest

/**
 * Placement seam: decide which node should run a replica.
 *
 * Implementations must not depend on Control HTTP, reconcile, or repository
 * types so this module can be extracted to a standalone service later.
 */
interface Scheduler {
    fun place(request: PlacementRequest): PlacementDecision
}

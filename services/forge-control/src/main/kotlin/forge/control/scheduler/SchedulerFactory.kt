package forge.control.scheduler

import forge.control.telemetry.Telemetry

/**
 * Select a [Scheduler] implementation from `FORGE_SCHEDULER_STRATEGY`,
 * composing anti-affinity filtering when a [PlacementStore] is provided.
 */
object SchedulerFactory {
    const val STRATEGY_FIRST_FIT: String = "first-fit"
    const val STRATEGY_LEAST_ALLOCATED: String = "least-allocated"
    const val STRATEGY_SINGLE_NODE: String = "single-node"

    val SUPPORTED: Set<String> = setOf(
        STRATEGY_FIRST_FIT,
        STRATEGY_LEAST_ALLOCATED,
        STRATEGY_SINGLE_NODE,
    )

    fun create(
        strategy: String,
        nodeStore: NodeStore,
        reservation: CapacityReservation,
        localNodeId: String,
        schedulerEnabled: Boolean,
        placementStore: PlacementStore? = null,
        telemetry: Telemetry = Telemetry.current(),
        strictNodeSelector: Boolean = false,
    ): Scheduler {
        if (!schedulerEnabled) {
            return SingleNodeScheduler(nodeId = null)
        }
        val antiAffinity = placementStore?.let { AntiAffinityFilter(it) }
            ?: AntiAffinityFilter.noop()
        val onSoftFallback: () -> Unit = { telemetry.recordAntiAffinityFallback() }
        return when (strategy) {
            STRATEGY_FIRST_FIT -> FirstFitScheduler(
                nodeStore,
                reservation,
                antiAffinity,
                onSoftFallback,
                strictNodeSelector,
            )
            STRATEGY_LEAST_ALLOCATED -> LeastAllocatedScheduler(
                nodeStore,
                reservation,
                antiAffinity,
                onSoftFallback,
                strictNodeSelector,
            )
            STRATEGY_SINGLE_NODE -> SingleNodeScheduler(
                availableNodes = {
                    val online = nodeStore.listOnlineIds()
                    if (online.isNotEmpty()) {
                        online
                    } else {
                        listOfNotNull(localNodeId.trim().takeIf { it.isNotEmpty() })
                    }
                },
            )
            else -> throw IllegalArgumentException(
                "FORGE_SCHEDULER_STRATEGY must be ${SUPPORTED.joinToString("|")}, got '$strategy'",
            )
        }
    }
}

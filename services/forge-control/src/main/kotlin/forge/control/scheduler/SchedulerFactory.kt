package forge.control.scheduler

/**
 * Select a [Scheduler] implementation from `FORGE_SCHEDULER_STRATEGY`.
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
    ): Scheduler {
        if (!schedulerEnabled) {
            return SingleNodeScheduler(nodeId = null)
        }
        return when (strategy) {
            STRATEGY_FIRST_FIT -> FirstFitScheduler(nodeStore, reservation)
            STRATEGY_LEAST_ALLOCATED -> LeastAllocatedScheduler(nodeStore, reservation)
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

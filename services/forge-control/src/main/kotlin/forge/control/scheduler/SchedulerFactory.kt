package forge.control.scheduler

import forge.control.scheduler.model.WhenUnsatisfiable
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
        topologySpreadDefault: WhenUnsatisfiable = WhenUnsatisfiable.DoNotSchedule,
        volumeLocality: VolumeLocalityStore = InMemoryVolumeLocalityStore(),
        log: forge.control.logging.JsonLog? = null,
    ): Scheduler {
        if (!schedulerEnabled) {
            return SingleNodeScheduler(nodeId = null)
        }
        val antiAffinity = placementStore?.let { AntiAffinityFilter(it) }
            ?: AntiAffinityFilter.noop()
        val workloadAffinity = placementStore?.let { WorkloadAffinityFilter(nodeStore, it) }
            ?: WorkloadAffinityFilter.noop()
        val topologySpread = placementStore?.let {
            TopologySpreadFilter(nodeStore, it, topologySpreadDefault)
        } ?: TopologySpreadFilter.noop()
        val placedReplicas: () -> List<Placement> = {
            placementStore?.listPlaced().orEmpty()
        }
        val statefulFilter = StatefulPlacementFilter(
            volumeLocality = volumeLocality,
            placedReplicas = placedReplicas,
            log = log,
        )
        val onSoftFallback: () -> Unit = { telemetry.recordAntiAffinityFallback() }
        return when (strategy) {
            STRATEGY_FIRST_FIT -> FirstFitScheduler(
                nodes = nodeStore,
                reservation = reservation,
                antiAffinity = antiAffinity,
                onSoftFallback = onSoftFallback,
                strictNodeSelector = strictNodeSelector,
                workloadAffinity = workloadAffinity,
                topologySpread = topologySpread,
                placedReplicas = placedReplicas,
                statefulFilter = statefulFilter,
            )
            STRATEGY_LEAST_ALLOCATED -> LeastAllocatedScheduler(
                nodes = nodeStore,
                reservation = reservation,
                antiAffinity = antiAffinity,
                onSoftFallback = onSoftFallback,
                strictNodeSelector = strictNodeSelector,
                workloadAffinity = workloadAffinity,
                topologySpread = topologySpread,
                placedReplicas = placedReplicas,
                statefulFilter = statefulFilter,
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

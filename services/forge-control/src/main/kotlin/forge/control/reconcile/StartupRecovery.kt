package forge.control.reconcile

import forge.control.logging.JsonLog
import java.util.UUID

data class StartupRecoveryResult(
    val resumedDeployments: Int,
    val adoptedContainers: Int,
    val completedOnStartup: Int,
    val orphanedStopped: Int,
)

/**
 * Rebuilds controller intent from persisted desired/actual + history on Control start.
 * Adopts existing forge workloads by label-equivalent id; GCs orphans.
 */
class StartupRecovery(
    private val deploymentStore: DeploymentStore,
    private val runtimeClient: RuntimeClient,
    private val transitionRecorder: TransitionRecorder,
    private val lastHealthyStore: LastHealthyStore = InMemoryLastHealthyStore(),
    private val healthEvaluator: HealthEvaluator = HealthEvaluator(),
    private val rollbacker: Rollbacker = Rollbacker(),
    private val log: JsonLog,
    private val adoptLabels: Boolean = true,
) {
    fun recover(): StartupRecoveryResult {
        if (!adoptLabels) {
            log.info("startup recovery skipped", "adopt_labels" to false)
            return StartupRecoveryResult(0, 0, 0, 0)
        }

        var resumed = 0
        var adopted = 0
        var completed = 0

        val inFlight = deploymentStore.listDesired().filter { desired ->
            val status = deploymentStore.getStatus(UUID.fromString(desired.deploymentId))
            status in IN_FLIGHT
        }

        for (desired in inFlight) {
            val deploymentId = UUID.fromString(desired.deploymentId)
            val status = DeploymentLifecycle.parse(deploymentStore.getStatus(deploymentId))
            resumed++

            val actual = try {
                runtimeClient.observe(deploymentId)
            } catch (e: RuntimeUnreachableException) {
                log.warn(
                    "startup recovery runtime unreachable",
                    "deployment_id" to desired.deploymentId,
                    "error" to (e.message ?: e.javaClass.simpleName),
                )
                continue
            }

            adopted += actual.replicas.count {
                it.statusEnum() in setOf(ReplicaStatus.Pending, ReplicaStatus.Running, ReplicaStatus.Ready)
            }

            when (status) {
                DeploymentLifecycle.Deploying -> {
                    val health = healthEvaluator.evaluate(desired, actual, timedOut = false)
                    if (health == RolloutHealth.Success) {
                        transitionRecorder.transition(
                            deploymentId = deploymentId,
                            to = DeploymentLifecycle.Deployed,
                            from = DeploymentLifecycle.Deploying,
                            image = desired.image,
                            desiredReplicas = desired.replicas,
                            actualReplicas = actual.replicas.size,
                            reason = "startup: all replicas ready",
                        )
                        serviceKey(desired)?.let { key ->
                            lastHealthyStore.put(
                                LastHealthyDeployment(
                                    serviceId = key,
                                    deploymentId = deploymentId,
                                    image = desired.image,
                                    replicas = desired.replicas,
                                ),
                            )
                        }
                        completed++
                        log.info(
                            "startup recovery completed deploying",
                            "deployment_id" to desired.deploymentId,
                            "status" to "deployed",
                        )
                    } else {
                        log.info(
                            "startup recovery resume deploying",
                            "deployment_id" to desired.deploymentId,
                            "actual_replicas" to actual.replicas.size,
                        )
                    }
                }
                DeploymentLifecycle.RollingBack -> {
                    val lastHealthy = serviceKey(desired)?.let { lastHealthyStore.get(it) }
                    if (lastHealthy != null &&
                        rollbacker.isRestored(
                            actual,
                            lastHealthy.copy(replicas = maxOf(lastHealthy.replicas, desired.replicas)),
                        )
                    ) {
                        transitionRecorder.transition(
                            deploymentId = deploymentId,
                            to = DeploymentLifecycle.RolledBack,
                            from = DeploymentLifecycle.RollingBack,
                            image = lastHealthy.image,
                            desiredReplicas = maxOf(lastHealthy.replicas, desired.replicas),
                            actualReplicas = actual.replicas.size,
                            reason = "startup: rollback already restored",
                        )
                        completed++
                        log.info(
                            "startup recovery completed rollback",
                            "deployment_id" to desired.deploymentId,
                            "status" to "rolled_back",
                        )
                    } else {
                        log.info(
                            "startup recovery resume rolling_back",
                            "deployment_id" to desired.deploymentId,
                            "actual_replicas" to actual.replicas.size,
                        )
                    }
                }
                else -> Unit
            }
        }

        val orphanedStopped = garbageCollectOrphans()

        log.info(
            "startup recovery complete",
            "adopted_containers" to adopted,
            "resumed_deployments" to resumed,
            "completed_on_startup" to completed,
            "orphaned_stopped" to orphanedStopped,
        )

        return StartupRecoveryResult(
            resumedDeployments = resumed,
            adoptedContainers = adopted,
            completedOnStartup = completed,
            orphanedStopped = orphanedStopped,
        )
    }

    /**
     * Stop workloads whose runtime id embeds a deployment short id with no live deployment.
     */
    fun garbageCollectOrphans(): Int {
        val liveShorts = deploymentStore.listDesired()
            .map { WorkloadNamer.deploymentShort(UUID.fromString(it.deploymentId)) }
            .toSet()

        val workloads = try {
            runtimeClient.listWorkloads()
        } catch (e: RuntimeUnreachableException) {
            log.warn(
                "startup orphan GC runtime unreachable",
                "error" to (e.message ?: e.javaClass.simpleName),
            )
            return 0
        }

        var stopped = 0
        for (workload in workloads) {
            val runtimeId = workload.runtimeDeploymentId
            // Only consider forge-style replica ids: <slug>-<short8>-<index>
            val short = extractDeploymentShort(runtimeId) ?: continue
            if (short in liveShorts) continue
            try {
                log.info(
                    "startup orphan GC stopping",
                    "runtime_deployment_id" to runtimeId,
                    "deployment_short" to short,
                )
                runtimeClient.stopWorkload(runtimeId)
                stopped++
            } catch (e: Exception) {
                log.warn(
                    "startup orphan GC stop failed",
                    "runtime_deployment_id" to runtimeId,
                    "error" to (e.message ?: e.javaClass.simpleName),
                )
            }
        }
        return stopped
    }

    /** Classifies persisted status for tests / recovery decisions. */
    fun classify(status: String, actualReadyForDesired: Boolean, rollbackRestored: Boolean): String =
        when (DeploymentLifecycle.parse(status)) {
            DeploymentLifecycle.Deploying ->
                if (actualReadyForDesired) DeploymentLifecycle.Deployed.wire()
                else DeploymentLifecycle.Deploying.wire()
            DeploymentLifecycle.RollingBack ->
                if (rollbackRestored) DeploymentLifecycle.RolledBack.wire()
                else DeploymentLifecycle.RollingBack.wire()
            else -> DeploymentLifecycle.parse(status).wire()
        }

    private fun serviceKey(desired: DesiredState): UUID? =
        desired.serviceId.takeIf { it.isNotBlank() }?.let { runCatching { UUID.fromString(it) }.getOrNull() }

    companion object {
        private val IN_FLIGHT = setOf(
            DeploymentLifecycle.Deploying.wire(),
            DeploymentLifecycle.RollingBack.wire(),
        )

        /** Extract 8-char deployment short from `<slug>-<short>-<index>`. */
        fun extractDeploymentShort(runtimeDeploymentId: String): String? {
            val parts = runtimeDeploymentId.split('-')
            if (parts.size < 3) return null
            val short = parts[parts.size - 2]
            return short.takeIf { it.length == 8 && it.all { ch -> ch.isDigit() || ch in 'a'..'f' } }
        }
    }
}

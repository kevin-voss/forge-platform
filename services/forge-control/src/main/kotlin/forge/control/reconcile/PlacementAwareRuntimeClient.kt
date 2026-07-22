package forge.control.reconcile

import forge.control.scheduler.NodeStore
import forge.control.scheduler.PlacementStore
import java.util.concurrent.ConcurrentHashMap

/**
 * Routes [ensureWorkload] to the Runtime agent address recorded on the active
 * placement so containers are labeled with the scheduled [forge.node_id].
 * Observe/stop/list stay on the fallback client (shared Docker socket demos
 * can still see or remove workloads via any agent).
 */
class PlacementAwareRuntimeClient(
    private val fallback: RuntimeClient,
    private val nodeStore: NodeStore,
    private val placementStore: PlacementStore,
    private val clientFactory: (String) -> RuntimeClient = { HttpRuntimeClient(it) },
) : RuntimeClient {
    private val clients = ConcurrentHashMap<String, RuntimeClient>()

    override fun loadActual(deploymentId: java.util.UUID): ActualState =
        fallback.loadActual(deploymentId)

    override fun observe(deploymentId: java.util.UUID): ActualState =
        fallback.observe(deploymentId)

    override fun findWorkload(runtimeDeploymentId: String): WorkloadHandle? =
        fallback.findWorkload(runtimeDeploymentId)

    override fun ensureWorkload(request: WorkloadEnsureRequest): EnsureOutcome {
        val placement = placementStore.find(request.deploymentId, request.replicaIndex)
        val address = placement?.nodeId
            ?.takeIf { it.isNotBlank() }
            ?.let { nodeStore.find(it)?.address }
            ?.trim()
            .orEmpty()
        val client = if (address.startsWith("http://") || address.startsWith("https://")) {
            clients.computeIfAbsent(address, clientFactory)
        } else {
            fallback
        }
        return client.ensureWorkload(request)
    }

    override fun drainWorkload(runtimeDeploymentId: String) {
        fallback.drainWorkload(runtimeDeploymentId)
    }

    override fun stopWorkload(runtimeDeploymentId: String) {
        fallback.stopWorkload(runtimeDeploymentId)
    }

    override fun listWorkloads(): List<WorkloadHandle> = fallback.listWorkloads()
}

package forge.control.reconcile

import forge.control.scheduler.NodeStore
import forge.control.scheduler.PendingQueue
import forge.control.scheduler.PlacementStore
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap

/**
 * Routes workload mutations to the Runtime agent address recorded on the active
 * placement so containers are labeled with the scheduled [forge.node_id].
 *
 * Observation fans out across placed nodes (provider-created or seed) and merges
 * results so reconciler readiness works without a special seed Runtime. Stop/list
 * still prefer the fallback client when present (shared Docker socket demos), and
 * otherwise the first reachable placed-node agent.
 */
class PlacementAwareRuntimeClient(
    private val fallback: RuntimeClient,
    private val nodeStore: NodeStore,
    private val placementStore: PlacementStore,
    private val clientFactory: (String) -> RuntimeClient = { HttpRuntimeClient(it) },
) : RuntimeClient {
    private val clients = ConcurrentHashMap<String, RuntimeClient>()

    override fun loadActual(deploymentId: UUID): ActualState = observe(deploymentId)

    override fun observe(deploymentId: UUID): ActualState {
        val merged = linkedMapOf<String, ReplicaObservation>()
        for (address in placedAddresses(deploymentId)) {
            val client = clients.computeIfAbsent(address, clientFactory)
            try {
                for (replica in client.observe(deploymentId).replicas) {
                    merged.putIfAbsent(replicaKey(replica), replica)
                }
            } catch (_: Exception) {
                // Node briefly unreachable — keep trying others / fallback.
            }
        }
        try {
            for (replica in fallback.observe(deploymentId).replicas) {
                merged.putIfAbsent(replicaKey(replica), replica)
            }
        } catch (_: Exception) {
            // Seed / fallback Runtime is optional for provider-only fleets.
        }
        return ActualState(merged.values.sortedBy { it.replicaIndex ?: Int.MAX_VALUE })
    }

    override fun findWorkload(runtimeDeploymentId: String): WorkloadHandle? {
        for (address in allKnownAddresses()) {
            val client = clients.computeIfAbsent(address, clientFactory)
            try {
                val found = client.findWorkload(runtimeDeploymentId)
                if (found != null) return found
            } catch (_: Exception) {
                // try next
            }
        }
        return try {
            fallback.findWorkload(runtimeDeploymentId)
        } catch (_: Exception) {
            null
        }
    }

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
        routeMutation(runtimeDeploymentId) { it.drainWorkload(runtimeDeploymentId) }
    }

    override fun stopWorkload(runtimeDeploymentId: String) {
        routeMutation(runtimeDeploymentId) { it.stopWorkload(runtimeDeploymentId) }
    }

    override fun listWorkloads(): List<WorkloadHandle> {
        val merged = linkedMapOf<String, WorkloadHandle>()
        for (address in allKnownAddresses()) {
            val client = clients.computeIfAbsent(address, clientFactory)
            try {
                for (handle in client.listWorkloads()) {
                    merged.putIfAbsent(handle.runtimeDeploymentId, handle)
                }
            } catch (_: Exception) {
                // try next
            }
        }
        try {
            for (handle in fallback.listWorkloads()) {
                merged.putIfAbsent(handle.runtimeDeploymentId, handle)
            }
        } catch (_: Exception) {
            // optional
        }
        return merged.values.toList()
    }

    private fun routeMutation(runtimeDeploymentId: String, action: (RuntimeClient) -> Unit) {
        var lastError: Exception? = null
        for (address in allKnownAddresses()) {
            val client = clients.computeIfAbsent(address, clientFactory)
            try {
                action(client)
                return
            } catch (e: Exception) {
                lastError = e
            }
        }
        try {
            action(fallback)
        } catch (e: Exception) {
            throw lastError ?: e
        }
    }

    private fun placedAddresses(deploymentId: UUID): List<String> =
        placementStore.listByDeployment(deploymentId, PendingQueue.STATUS_PLACED)
            .mapNotNull { placement ->
                placement.nodeId
                    ?.takeIf { it.isNotBlank() }
                    ?.let { nodeStore.find(it)?.address }
                    ?.trim()
                    ?.takeIf { it.startsWith("http://") || it.startsWith("https://") }
            }
            .distinct()

    private fun allKnownAddresses(): List<String> =
        nodeStore.listOnlineIds()
            .mapNotNull { id ->
                nodeStore.find(id)?.address
                    ?.trim()
                    ?.takeIf { it.startsWith("http://") || it.startsWith("https://") }
            }
            .distinct()

    private fun replicaKey(replica: ReplicaObservation): String =
        replica.replicaIndex?.let { "idx:$it" } ?: "id:${replica.replicaId}"
}

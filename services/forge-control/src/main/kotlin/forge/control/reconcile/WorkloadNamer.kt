package forge.control.reconcile

import java.util.UUID

/**
 * Deterministic workload identity for reconcile create/stop.
 *
 * Runtime names containers `forge-<deployment_id>`, so the Runtime API
 * `deployment_id` is the suffix after `forge-`:
 * `<service_slug>-<deployment_short>-<replica_index>`.
 */
object WorkloadNamer {
    fun serviceSlug(raw: String): String {
        val slug = raw.trim().lowercase()
            .replace(Regex("[^a-z0-9]+"), "-")
            .trim('-')
            .take(32)
        return slug.ifEmpty { "svc" }
    }

    fun deploymentShort(deploymentId: UUID): String =
        deploymentId.toString().replace("-", "").take(8)

    fun runtimeDeploymentId(
        serviceSlug: String,
        deploymentId: UUID,
        replicaIndex: Int,
    ): String {
        require(replicaIndex >= 0) { "replicaIndex must be >= 0" }
        return "${serviceSlug(serviceSlug)}-${deploymentShort(deploymentId)}-$replicaIndex"
    }

    fun containerName(
        serviceSlug: String,
        deploymentId: UUID,
        replicaIndex: Int,
    ): String = "forge-${runtimeDeploymentId(serviceSlug, deploymentId, replicaIndex)}"

    fun labels(
        deploymentId: UUID,
        serviceId: String,
        replicaIndex: Int,
        image: String,
    ): Map<String, String> =
        mapOf(
            "forge.deployment" to deploymentId.toString(),
            "forge.service" to serviceId,
            "forge.replica" to replicaIndex.toString(),
            "forge.image" to image,
        )

    /** Parse replica index from a Runtime deployment id or compact replica id. */
    fun parseReplicaIndex(runtimeDeploymentId: String?): Int? {
        if (runtimeDeploymentId.isNullOrBlank()) return null
        runtimeDeploymentId.toIntOrNull()?.takeIf { it >= 0 }?.let { return it }
        val idx = runtimeDeploymentId.substringAfterLast('-', missingDelimiterValue = "")
        return idx.toIntOrNull()?.takeIf { it >= 0 }
            ?: runtimeDeploymentId.removePrefix("r").toIntOrNull()?.takeIf { it >= 0 }
    }

    fun matchesDeployment(runtimeDeploymentId: String, deploymentId: UUID): Boolean =
        runtimeDeploymentId.contains(deploymentShort(deploymentId))
}

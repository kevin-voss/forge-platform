package forge.control.domain

import java.time.Instant
import java.util.UUID

data class Project(
    val id: UUID,
    val name: String,
    val slug: String,
    val createdAt: Instant,
    val updatedAt: Instant,
) {
    init {
        require(name.isNotBlank()) { "name must not be blank" }
        require(slug.isNotBlank()) { "slug must not be blank" }
    }
}

data class Environment(
    val id: UUID,
    val projectId: UUID,
    val name: String,
    val createdAt: Instant,
    val updatedAt: Instant,
) {
    init {
        require(name.isNotBlank()) { "name must not be blank" }
    }
}

data class Application(
    val id: UUID,
    val projectId: UUID,
    val name: String,
    val createdAt: Instant,
    val updatedAt: Instant,
) {
    init {
        require(name.isNotBlank()) { "name must not be blank" }
    }
}

data class Service(
    val id: UUID,
    val applicationId: UUID,
    val name: String,
    val port: Int,
    val createdAt: Instant,
    val updatedAt: Instant,
    val image: String? = null,
    val imageDigest: String? = null,
    val imageCommit: String? = null,
    val imageBuildId: String? = null,
    val lastHealthyDeploymentId: UUID? = null,
    val lastHealthyImage: String? = null,
    val lastHealthyReplicas: Int? = null,
) {
    init {
        require(name.isNotBlank()) { "name must not be blank" }
        require(port in 1..65535) { "port must be 1–65535" }
        if (image != null) {
            require(image.isNotBlank()) { "image must not be blank when set" }
        }
        if (lastHealthyImage != null) {
            require(lastHealthyImage.isNotBlank()) { "last_healthy_image must not be blank when set" }
        }
        if (lastHealthyReplicas != null) {
            require(lastHealthyReplicas >= 0) { "last_healthy_replicas must be >= 0" }
        }
    }
}

data class Deployment(
    val id: UUID,
    val serviceId: UUID,
    val environmentId: UUID,
    val image: String,
    val desiredReplicas: Int,
    val status: String,
    val createdAt: Instant,
    val updatedAt: Instant,
    val rolloutBatchSize: Int = 1,
    val rolloutTimeoutSeconds: Int = 120,
    /** Stable resource name (backfilled from service name in 20.07). */
    val name: String = "",
) {
    init {
        require(image.isNotBlank()) { "image must not be blank" }
        require(desiredReplicas >= 0) { "desired_replicas must be >= 0" }
        require(status in DEPLOYMENT_STATUSES) { "invalid deployment status" }
        require(rolloutBatchSize >= 1) { "rollout_batch_size must be >= 1" }
        require(rolloutTimeoutSeconds >= 1) { "rollout_timeout_s must be >= 1" }
        if (name.isNotEmpty()) {
            require(name.isNotBlank()) { "name must not be blank when set" }
        }
    }

    private companion object {
        val DEPLOYMENT_STATUSES = setOf(
            "pending",
            "active",
            "failed",
            "stopped",
            "deploying",
            "deployed",
            "rolling_back",
            "rolled_back",
        )
    }
}

data class AuditEntry(
    val id: UUID,
    val entityType: String,
    val entityId: UUID,
    val action: String,
    val actor: String,
    val at: Instant,
    val detailJson: String,
) {
    init {
        require(entityType.isNotBlank()) { "entity_type must not be blank" }
        require(action.isNotBlank()) { "action must not be blank" }
        require(actor.isNotBlank()) { "actor must not be blank" }
    }
}

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
) {
    init {
        require(name.isNotBlank()) { "name must not be blank" }
        require(port in 1..65535) { "port must be 1–65535" }
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
) {
    init {
        require(image.isNotBlank()) { "image must not be blank" }
        require(desiredReplicas >= 0) { "desired_replicas must be >= 0" }
        require(status.isNotBlank()) { "status must not be blank" }
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

package forge.control.manageddb

import java.time.Instant
import java.util.UUID

/** Lifecycle status for managed database instances. */
enum class DbInstanceStatus(val wire: String) {
    Provisioning("provisioning"),
    Available("available"),
    Error("error"),
    Deleting("deleting"),
    ;

    companion object {
        fun parse(raw: String): DbInstanceStatus =
            entries.firstOrNull { it.wire == raw }
                ?: throw IllegalArgumentException("invalid db instance status: $raw")
    }
}

/** Lifecycle status for databases on an instance. */
enum class DbDatabaseStatus(val wire: String) {
    Provisioning("provisioning"),
    Available("available"),
    Error("error"),
    ;

    companion object {
        fun parse(raw: String): DbDatabaseStatus =
            entries.firstOrNull { it.wire == raw }
                ?: throw IllegalArgumentException("invalid db database status: $raw")
    }
}

data class DbInstance(
    val id: UUID,
    val projectId: UUID,
    val name: String,
    val status: DbInstanceStatus,
    val engine: String = "postgres",
    val deletionProtection: Boolean = true,
    val statusReason: String? = null,
    /** Opaque provisioner endpoint reference — never Control's own JDBC URL. */
    val endpointRef: String? = null,
    val host: String? = null,
    val port: Int? = null,
    val containerId: String? = null,
    val createdAt: Instant,
    val updatedAt: Instant,
) {
    init {
        require(name.isNotBlank()) { "name must not be blank" }
        require(engine.isNotBlank()) { "engine must not be blank" }
    }
}

data class DbDatabase(
    val id: UUID,
    val instanceId: UUID,
    val name: String,
    val status: DbDatabaseStatus = DbDatabaseStatus.Provisioning,
    val statusReason: String? = null,
    val createdAt: Instant,
) {
    init {
        require(name.isNotBlank()) { "name must not be blank" }
    }
}

data class DbCredential(
    val id: UUID,
    val databaseId: UUID,
    val username: String,
    val secretRef: String?,
    val status: String,
    val createdAt: Instant,
) {
    init {
        require(username.isNotBlank()) { "username must not be blank" }
        require(status.isNotBlank()) { "status must not be blank" }
    }
}

data class DbAttachment(
    val id: UUID,
    val databaseId: UUID,
    val applicationId: UUID,
    val envVar: String,
    /** Secrets reference for the composed connection URL — never plaintext. */
    val secretRef: String?,
    val createdAt: Instant,
) {
    init {
        require(envVar.isNotBlank()) { "env_var must not be blank" }
    }
}

data class DbBackup(
    val id: UUID,
    val databaseId: UUID,
    val location: String?,
    val status: String,
    val createdAt: Instant,
) {
    init {
        require(status.isNotBlank()) { "status must not be blank" }
    }
}

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

/** Lifecycle status for on-demand database backups. */
enum class DbBackupStatus(val wire: String) {
    Pending("pending"),
    Running("running"),
    Succeeded("succeeded"),
    Failed("failed"),
    ;

    companion object {
        fun parse(raw: String): DbBackupStatus =
            entries.firstOrNull { it.wire == raw }
                ?: throw IllegalArgumentException("invalid db backup status: $raw")
    }
}

/** Lifecycle status for restore attempts against a backup. */
enum class DbRestoreStatus(val wire: String) {
    Running("running"),
    Succeeded("succeeded"),
    Failed("failed"),
    ;

    companion object {
        fun parse(raw: String): DbRestoreStatus =
            entries.firstOrNull { it.wire == raw }
                ?: throw IllegalArgumentException("invalid db restore status: $raw")
    }
}

data class DbBackup(
    val id: UUID,
    val databaseId: UUID,
    val location: String?,
    val status: DbBackupStatus,
    val checksum: String? = null,
    val sizeBytes: Long? = null,
    val statusReason: String? = null,
    val completedAt: Instant? = null,
    val restoreStatus: DbRestoreStatus? = null,
    val restoreTargetDatabaseId: UUID? = null,
    val restoreStatusReason: String? = null,
    val restoreCompletedAt: Instant? = null,
    val createdAt: Instant,
)

/** Result of a provisioner dump (archive bytes + integrity metadata). */
data class DumpArchive(
    val bytes: ByteArray,
    val checksum: String,
    val sizeBytes: Long,
) {
    override fun equals(other: Any?): Boolean {
        if (this === other) return true
        if (other !is DumpArchive) return false
        return checksum == other.checksum && sizeBytes == other.sizeBytes && bytes.contentEquals(other.bytes)
    }

    override fun hashCode(): Int = 31 * checksum.hashCode() + sizeBytes.hashCode()
}

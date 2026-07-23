package forge.control.manageddb

import forge.control.logging.JsonLog
import forge.control.telemetry.Telemetry
import java.time.Instant
import java.util.UUID
import java.util.concurrent.Executor
import java.util.concurrent.Executors

/**
 * Runs on-demand backups: dump → archive store → checksum → status update.
 */
class BackupRunner(
    private val store: ManagedDbRepository,
    private val provisioner: Provisioner,
    private val archives: ArchiveStore,
    private val log: JsonLog? = null,
    private val telemetry: Telemetry = Telemetry.current(),
    private val executor: Executor = Executors.newCachedThreadPool { r ->
        Thread(r, "managed-db-backup").apply { isDaemon = true }
    },
) {
    fun enqueue(backupId: UUID) {
        executor.execute {
            try {
                run(backupId)
            } catch (e: Exception) {
                log?.error(
                    "managed db backup runner crashed",
                    "backup_id" to backupId,
                    "error" to (e.message ?: e.javaClass.simpleName),
                )
            }
        }
    }

    fun run(backupId: UUID) {
        val backup = store.findBackupById(backupId) ?: return
        val database = store.findDatabaseById(backup.databaseId) ?: run {
            store.updateBackup(
                backupId,
                status = DbBackupStatus.Failed,
                statusReason = "database not found",
                completedAt = Instant.now(),
            )
            telemetry.recordManagedDbBackup("failed")
            return
        }
        val instance = store.findInstanceById(database.instanceId) ?: run {
            store.updateBackup(
                backupId,
                status = DbBackupStatus.Failed,
                statusReason = "instance not found",
                completedAt = Instant.now(),
            )
            telemetry.recordManagedDbBackup("failed")
            return
        }
        rememberEndpoint(instance)
        store.updateBackup(backupId, status = DbBackupStatus.Running)
        val started = System.nanoTime()
        var location: String? = null
        try {
            log?.info(
                "managed db backup start",
                "backup_id" to backupId,
                "database_id" to database.id,
                "instance_id" to instance.id,
            )
            val dump = provisioner.dumpDatabase(instance.id, database.name)
            location = archives.put(instance.projectId, backupId, dump.bytes)
            store.updateBackup(
                backupId,
                status = DbBackupStatus.Succeeded,
                location = location,
                checksum = dump.checksum,
                sizeBytes = dump.sizeBytes,
                completedAt = Instant.now(),
            )
            val durationMs = (System.nanoTime() - started) / 1_000_000
            telemetry.recordManagedDbBackup("succeeded")
            log?.info(
                "managed db backup end",
                "backup_id" to backupId,
                "database_id" to database.id,
                "size_bytes" to dump.sizeBytes,
                "duration_ms" to durationMs,
                "checksum" to dump.checksum,
                "location" to location,
            )
        } catch (e: Exception) {
            location?.let { archives.delete(instance.projectId, it) }
            val reason = e.message ?: e.javaClass.simpleName
            store.updateBackup(
                backupId,
                status = DbBackupStatus.Failed,
                statusReason = reason,
                completedAt = Instant.now(),
            )
            telemetry.recordManagedDbBackup("failed")
            log?.error(
                "managed db backup failed",
                "backup_id" to backupId,
                "database_id" to database.id,
                "error" to reason,
            )
        }
    }

    private fun rememberEndpoint(instance: DbInstance) {
        val local = provisioner as? LocalProvisioner ?: return
        val host = instance.host ?: return
        val port = instance.port ?: return
        local.rememberEndpoint(
            instance.id,
            InstanceEndpoint(
                endpointRef = instance.endpointRef ?: "postgres://$host:$port/postgres",
                host = host,
                port = port,
                containerId = instance.containerId,
            ),
        )
    }
}

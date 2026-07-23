package forge.control.manageddb

import forge.control.logging.JsonLog
import forge.control.telemetry.Telemetry
import java.time.Instant
import java.util.UUID
import java.util.concurrent.Executor
import java.util.concurrent.Executors

/**
 * Runs restore: fetch archive → verify checksum → pg_restore into target.
 */
class RestoreRunner(
    private val store: ManagedDbRepository,
    private val provisioner: Provisioner,
    private val archives: ArchiveStore,
    private val log: JsonLog? = null,
    private val telemetry: Telemetry = Telemetry.current(),
    private val executor: Executor = Executors.newCachedThreadPool { r ->
        Thread(r, "managed-db-restore").apply { isDaemon = true }
    },
) {
    fun enqueue(backupId: UUID, targetDatabaseId: UUID) {
        executor.execute {
            try {
                run(backupId, targetDatabaseId)
            } catch (e: Exception) {
                log?.error(
                    "managed db restore runner crashed",
                    "backup_id" to backupId,
                    "target_database_id" to targetDatabaseId,
                    "error" to (e.message ?: e.javaClass.simpleName),
                )
            }
        }
    }

    fun run(backupId: UUID, targetDatabaseId: UUID) {
        val backup = store.findBackupById(backupId) ?: return
        val sourceDb = store.findDatabaseById(backup.databaseId)
        val sourceInstance = sourceDb?.let { store.findInstanceById(it.instanceId) }
        val targetDb = store.findDatabaseById(targetDatabaseId)
        val targetInstance = targetDb?.let { store.findInstanceById(it.instanceId) }

        if (sourceDb == null || sourceInstance == null || targetDb == null || targetInstance == null) {
            store.updateBackupRestore(
                backupId,
                restoreStatus = DbRestoreStatus.Failed,
                restoreTargetDatabaseId = targetDatabaseId,
                restoreStatusReason = "database or instance not found",
                restoreCompletedAt = Instant.now(),
            )
            telemetry.recordManagedDbRestore("failed")
            return
        }

        rememberEndpoint(targetInstance)
        store.updateBackupRestore(
            backupId,
            restoreStatus = DbRestoreStatus.Running,
            restoreTargetDatabaseId = targetDatabaseId,
        )
        val started = System.nanoTime()
        try {
            val location = backup.location
                ?: throw IntegrityError("backup has no archive location")
            val expected = backup.checksum
                ?: throw IntegrityError("backup has no checksum")
            val bytes = archives.get(sourceInstance.projectId, location)
                ?: throw IntegrityError("backup archive missing at $location")
            if (!BackupChecksum.verify(bytes, expected)) {
                throw IntegrityError("checksum mismatch for backup archive")
            }
            log?.info(
                "managed db restore start",
                "backup_id" to backupId,
                "target_database_id" to targetDatabaseId,
                "size_bytes" to bytes.size,
                "checksum" to expected,
            )
            provisioner.restoreDatabase(targetInstance.id, targetDb.name, bytes)
            store.updateBackupRestore(
                backupId,
                restoreStatus = DbRestoreStatus.Succeeded,
                restoreTargetDatabaseId = targetDatabaseId,
                restoreCompletedAt = Instant.now(),
            )
            val durationMs = (System.nanoTime() - started) / 1_000_000
            telemetry.recordManagedDbRestore("succeeded")
            log?.info(
                "managed db restore end",
                "backup_id" to backupId,
                "target_database_id" to targetDatabaseId,
                "duration_ms" to durationMs,
                "checksum" to expected,
            )
        } catch (e: IntegrityError) {
            store.updateBackupRestore(
                backupId,
                restoreStatus = DbRestoreStatus.Failed,
                restoreTargetDatabaseId = targetDatabaseId,
                restoreStatusReason = "integrity_error: ${e.message}",
                restoreCompletedAt = Instant.now(),
            )
            telemetry.recordManagedDbRestore("failed")
            log?.error(
                "managed db restore integrity error",
                "backup_id" to backupId,
                "target_database_id" to targetDatabaseId,
                "error" to e.message,
            )
        } catch (e: Exception) {
            val reason = e.message ?: e.javaClass.simpleName
            store.updateBackupRestore(
                backupId,
                restoreStatus = DbRestoreStatus.Failed,
                restoreTargetDatabaseId = targetDatabaseId,
                restoreStatusReason = reason,
                restoreCompletedAt = Instant.now(),
            )
            telemetry.recordManagedDbRestore("failed")
            log?.error(
                "managed db restore failed",
                "backup_id" to backupId,
                "target_database_id" to targetDatabaseId,
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

class IntegrityError(message: String) : RuntimeException(message)

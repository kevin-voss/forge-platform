package forge.control.manageddb

import forge.control.http.ApiException
import java.util.UUID

/**
 * Enforces deletion-protection and attachment guards for managed databases.
 *
 * Deletes require `deletion_protection=false` and `force=true`; otherwise `409`.
 * Attached databases block delete by default (detach first).
 */
object DeletionGuard {
    fun assertCanDelete(
        resourceKind: String,
        resourceId: UUID,
        deletionProtection: Boolean,
        force: Boolean,
    ) {
        if (deletionProtection) {
            throw ApiException.Conflict(
                "deletion protection is enabled; disable protection and retry with force=true",
                mapOf(
                    "resource" to resourceKind,
                    "id" to resourceId.toString(),
                    "deletionProtection" to "true",
                ),
            )
        }
        if (!force) {
            throw ApiException.Conflict(
                "delete requires force=true after disabling deletion protection",
                mapOf(
                    "resource" to resourceKind,
                    "id" to resourceId.toString(),
                    "force" to "false",
                ),
            )
        }
    }

    fun assertNoAttachments(databaseId: UUID, attachmentCount: Int) {
        if (attachmentCount > 0) {
            throw ApiException.Conflict(
                "database has attachments; detach before delete",
                mapOf(
                    "databaseId" to databaseId.toString(),
                    "attachments" to attachmentCount.toString(),
                ),
            )
        }
    }
}

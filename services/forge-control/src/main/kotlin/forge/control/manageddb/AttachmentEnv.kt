package forge.control.manageddb

/**
 * Resolved managed-DB attachment env vars for Runtime injection.
 * Values are transient — never persist or log them.
 */
sealed class AttachmentEnvResult {
    data class Ready(
        val env: Map<String, String>,
        /** Opaque fingerprint contribution (secret refs only — never values). */
        val fingerprint: String,
    ) : AttachmentEnvResult()

    data class Hold(val detail: String) : AttachmentEnvResult()

    data object Empty : AttachmentEnvResult()
}

/** Seam used by the reconciler to inject attached DATABASE_URL-style env vars. */
fun interface AttachmentEnvSource {
    fun resolveForApplication(applicationId: String): AttachmentEnvResult
}

object NoOpAttachmentEnvSource : AttachmentEnvSource {
    override fun resolveForApplication(applicationId: String): AttachmentEnvResult =
        AttachmentEnvResult.Empty
}

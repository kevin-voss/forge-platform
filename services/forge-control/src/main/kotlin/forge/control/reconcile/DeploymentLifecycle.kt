package forge.control.reconcile

/** Deployment lifecycle statuses owned by the reconciler (07.04). */
enum class DeploymentLifecycle {
    Pending,
    Deploying,
    Deployed,
    RollingBack,
    RolledBack,
    Failed,
    ;

    fun wire(): String =
        when (this) {
            Pending -> "pending"
            Deploying -> "deploying"
            Deployed -> "deployed"
            RollingBack -> "rolling_back"
            RolledBack -> "rolled_back"
            Failed -> "failed"
        }

    companion object {
        val WIRES = entries.map { it.wire() }.toSet()

        fun parse(raw: String?): DeploymentLifecycle =
            when (raw?.trim()?.lowercase()) {
                null, "", "pending" -> Pending
                "deploying" -> Deploying
                "deployed", "active" -> Deployed
                "rolling_back" -> RollingBack
                "rolled_back" -> RolledBack
                "failed" -> Failed
                "stopped" -> Failed
                else -> Pending
            }
    }
}

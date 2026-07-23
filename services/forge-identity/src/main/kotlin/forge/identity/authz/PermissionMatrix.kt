package forge.identity.authz

/**
 * Data-driven `(action → allowed roles)` permission matrix.
 *
 * Deny-by-default: actions absent from the map (or with empty role sets) are denied.
 * Version is informational (`FORGE_AUTHZ_MATRIX_VERSION`); the matrix itself is code.
 */
data class PermissionMatrix(
    val version: String,
    private val grants: Map<String, Set<Role>>,
) {
    fun knownActions(): Set<String> = grants.keys

    fun allowedRoles(action: String): Set<Role>? = grants[action]

    fun isKnownAction(action: String): Boolean = grants.containsKey(action)

    fun allows(action: String, role: Role): Boolean {
        if (role == Role.NONE) return false
        val allowed = grants[action] ?: return false
        return role in allowed
    }

    /** Wire form: action → sorted role names (stable for docs/tests). */
    fun toWireMap(): Map<String, List<String>> =
        grants.entries
            .sortedBy { it.key }
            .associate { (action, roles) ->
                action to roles.map { it.wire }.sorted()
            }

    companion object {
        const val DEFAULT_VERSION: String = "1"

        private val OWNER_ADMIN = setOf(Role.ORGANIZATION_OWNER, Role.PROJECT_ADMIN)
        private val OWNER_ADMIN_DEV = OWNER_ADMIN + Role.DEVELOPER
        private val OWNER_ADMIN_DEV_SA = OWNER_ADMIN_DEV + Role.SERVICE_ACCOUNT
        private val ALL_PROJECT = OWNER_ADMIN_DEV_SA + Role.VIEWER

        /**
         * Initial platform matrix covering Control mutation categories plus secrets/config
         * actions Identity will evaluate for later epics.
         */
        fun default(version: String = DEFAULT_VERSION): PermissionMatrix {
            val grants = linkedMapOf(
                // Project / environment / application / service
                "project.read" to ALL_PROJECT,
                "project.write" to OWNER_ADMIN,
                "project.delete" to OWNER_ADMIN,
                "environment.read" to ALL_PROJECT,
                "environment.write" to OWNER_ADMIN_DEV,
                "application.read" to ALL_PROJECT,
                "application.write" to OWNER_ADMIN_DEV,
                "service.read" to ALL_PROJECT,
                "service.write" to OWNER_ADMIN_DEV,
                // Deployments
                "deployment.read" to ALL_PROJECT,
                "deployment.create" to OWNER_ADMIN_DEV_SA,
                "deployment.update" to OWNER_ADMIN_DEV_SA,
                "deployment.delete" to OWNER_ADMIN,
                // Secrets / config (epic 10)
                "secret.read" to OWNER_ADMIN_DEV_SA,
                "secret.write" to OWNER_ADMIN_DEV,
                "config.read" to ALL_PROJECT,
                "config.write" to OWNER_ADMIN_DEV,
                // Managed PostgreSQL (epic 18)
                "database.read" to ALL_PROJECT,
                "database.write" to OWNER_ADMIN_DEV,
                // Membership / tokens (least privilege for service-account)
                "member.manage" to OWNER_ADMIN,
                "token.manage" to OWNER_ADMIN,
            )
            return PermissionMatrix(version = version, grants = grants)
        }
    }
}

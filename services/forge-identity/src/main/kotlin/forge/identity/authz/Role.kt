package forge.identity.authz

/**
 * Canonical platform roles (specs.md Step 09).
 *
 * Hierarchy for human roles: organization-owner > project-admin > developer > viewer.
 * `service-account` is a distinct machine role (not in the human hierarchy).
 * `none` means no effective membership in the project scope.
 */
enum class Role(val wire: String) {
    ORGANIZATION_OWNER("organization-owner"),
    PROJECT_ADMIN("project-admin"),
    DEVELOPER("developer"),
    VIEWER("viewer"),
    SERVICE_ACCOUNT("service-account"),
    NONE("none"),
    ;

    /** Rank for human hierarchy comparisons; service-account and none are incomparable. */
    fun hierarchyRank(): Int? = when (this) {
        ORGANIZATION_OWNER -> 4
        PROJECT_ADMIN -> 3
        DEVELOPER -> 2
        VIEWER -> 1
        SERVICE_ACCOUNT, NONE -> null
    }

    fun dominates(other: Role): Boolean {
        val a = hierarchyRank() ?: return false
        val b = other.hierarchyRank() ?: return false
        return a >= b
    }

    companion object {
        private val byWire = entries.associateBy { it.wire }

        fun fromWire(value: String): Role? = byWire[value.trim()]

        /** Roles that may appear on membership rows (excludes [NONE]). */
        val membershipRoles: List<Role> = listOf(
            ORGANIZATION_OWNER,
            PROJECT_ADMIN,
            DEVELOPER,
            VIEWER,
            SERVICE_ACCOUNT,
        )
    }
}

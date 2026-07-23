package forge.control.resource

/**
 * Compile-time registration entry for a resource kind.
 *
 * [parentKind] is reserved for nested address shapes (e.g. Service under Application)
 * and is unused until step 20.07. [enforceScopeUniqueness] is activated in 20.07.
 *
 * [requiresDeleteConfirmation] — DELETE needs `X-Forge-Delete-Confirmation: <name>`.
 * [allowsCascade] — when a parent is deleted with `?cascade=foreground`, children of
 * this kind may be marked Terminating; default false (never silent cascade).
 */
data class KindDescriptor(
    val kind: String,
    val plural: String,
    val scope: ResourceScope,
    val parentKind: String? = null,
    val schemaVersion: Int,
    val owningController: String,
    val idPrefix: String,
    val requiresDeleteConfirmation: Boolean = false,
    val allowsCascade: Boolean = false,
    val enforceScopeUniqueness: Boolean = true,
) {
    init {
        require(kind.isNotBlank()) { "kind must not be blank" }
        require(plural.isNotBlank()) { "plural must not be blank" }
        require(idPrefix.isNotBlank()) { "idPrefix must not be blank" }
        require(owningController.isNotBlank()) { "owningController must not be blank" }
        require(schemaVersion >= 1) { "schemaVersion must be >= 1" }
        require(scope != ResourceScope.Cluster || parentKind == null) {
            "Cluster-scoped kinds cannot have a parentKind (got parentKind=$parentKind)"
        }
    }
}

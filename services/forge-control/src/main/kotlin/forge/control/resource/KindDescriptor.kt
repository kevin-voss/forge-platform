package forge.control.resource

/**
 * Compile-time registration entry for a resource kind.
 *
 * [parentKind] is reserved for nested address shapes (e.g. Service under Application)
 * and is unused until step 20.07. [requiresDeleteConfirmation] and
 * [enforceScopeUniqueness] are activated by later steps in this epic.
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

package forge.control.resource

import java.util.concurrent.ConcurrentHashMap

/**
 * In-memory catalog of resource kinds.
 *
 * ## Extension seam (epics 20–43)
 *
 * Product kinds register at process start via [register], or at controller
 * startup via the HTTP facade `POST /v1/kinds` ([registerIdempotent]). Look up
 * descriptors with [get] / [byPlural]. Storage for every kind goes through
 * [ResourceRepository]; step 20.07 may add a second implementation backed by
 * legacy tables without changing that interface.
 *
 * Wired into [forge.control.Application] / [forge.control.ControlServices] for the
 * generic HTTP surface (step 20.02+).
 */
class KindRegistry {
    private val byKind = ConcurrentHashMap<String, KindDescriptor>()
    private val byPluralName = ConcurrentHashMap<String, KindDescriptor>()

    fun register(descriptor: KindDescriptor) {
        val existingKind = byKind.putIfAbsent(descriptor.kind, descriptor)
        require(existingKind == null) {
            "kind already registered: ${descriptor.kind}"
        }
        val existingPlural = byPluralName.putIfAbsent(descriptor.plural, descriptor)
        if (existingPlural != null) {
            byKind.remove(descriptor.kind, descriptor)
            throw IllegalArgumentException(
                "plural already registered: ${descriptor.plural} (kind=${existingPlural.kind})",
            )
        }
    }

    /**
     * Idempotent registration for controller startup.
     *
     * Compatible when kind, plural, schemaVersion, and owningController match.
     * Scope "namespaced" (mapped to [ResourceScope.Environment] on the wire)
     * is treated as compatible with an existing [ResourceScope.Project] or
     * [ResourceScope.Environment] entry so Discovery can re-register the
     * shipped Service kind without conflicting with the Application-parent path.
     */
    fun registerIdempotent(descriptor: KindDescriptor): KindRegisterResult {
        val existingByKind = byKind[descriptor.kind]
        if (existingByKind != null) {
            return if (compatible(existingByKind, descriptor)) {
                KindRegisterResult.AlreadyRegistered(existingByKind)
            } else {
                KindRegisterResult.Conflict(
                    "kind already registered with conflicting descriptor: ${descriptor.kind}",
                )
            }
        }
        val existingByPlural = byPluralName[descriptor.plural]
        if (existingByPlural != null) {
            return if (compatible(existingByPlural, descriptor)) {
                KindRegisterResult.AlreadyRegistered(existingByPlural)
            } else {
                KindRegisterResult.Conflict(
                    "plural already registered: ${descriptor.plural} (kind=${existingByPlural.kind})",
                )
            }
        }
        return try {
            register(descriptor)
            KindRegisterResult.Created(descriptor)
        } catch (e: IllegalArgumentException) {
            // Race with another registrar: re-check compatibility.
            val raced = byKind[descriptor.kind] ?: byPluralName[descriptor.plural]
            if (raced != null && compatible(raced, descriptor)) {
                KindRegisterResult.AlreadyRegistered(raced)
            } else {
                KindRegisterResult.Conflict(e.message ?: "kind registration conflict")
            }
        }
    }

    fun get(kind: String): KindDescriptor? = byKind[kind]

    fun byPlural(plural: String): KindDescriptor? = byPluralName[plural]

    fun all(): Collection<KindDescriptor> = byKind.values.toList()

    companion object {
        internal fun compatible(existing: KindDescriptor, incoming: KindDescriptor): Boolean {
            if (existing.kind != incoming.kind) return false
            if (existing.plural != incoming.plural) return false
            if (existing.schemaVersion != incoming.schemaVersion) return false
            if (existing.owningController != incoming.owningController) return false
            if (existing.scope == incoming.scope) return true
            // namespaced wire value maps to Environment; allow Project|Environment match.
            val namespaced = setOf(ResourceScope.Project, ResourceScope.Environment)
            return existing.scope in namespaced && incoming.scope in namespaced
        }
    }
}

sealed class KindRegisterResult {
    data class Created(val descriptor: KindDescriptor) : KindRegisterResult()
    data class AlreadyRegistered(val descriptor: KindDescriptor) : KindRegisterResult()
    data class Conflict(val message: String) : KindRegisterResult()
}

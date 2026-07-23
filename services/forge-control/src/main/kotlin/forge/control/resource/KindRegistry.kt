package forge.control.resource

import java.util.concurrent.ConcurrentHashMap

/**
 * In-memory catalog of resource kinds.
 *
 * ## Extension seam (epics 20–43)
 *
 * Later steps and epics register product kinds via [register] at process start
 * (never from request handlers). Look up descriptors with [get] / [byPlural].
 * Storage for every kind goes through [ResourceRepository]; step 20.07 may add a
 * second implementation backed by legacy tables without changing that interface.
 *
 * This registry is intentionally not wired into [forge.control.Application] until
 * the generic HTTP surface lands in step 20.02.
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

    fun get(kind: String): KindDescriptor? = byKind[kind]

    fun byPlural(plural: String): KindDescriptor? = byPluralName[plural]

    fun all(): Collection<KindDescriptor> = byKind.values.toList()
}

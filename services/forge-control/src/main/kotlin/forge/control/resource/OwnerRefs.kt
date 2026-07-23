package forge.control.resource

import forge.control.http.ApiException
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonPrimitive

/** Parsed [metadata.ownerRefs] entry. */
data class OwnerRef(
    val kind: String,
    val id: String,
    val controller: Boolean? = null,
    val name: String? = null,
)

/**
 * Validates owner references: existence, same-or-wider scope, and cycle detection.
 */
object OwnerRefs {
    fun parse(ownerRefs: JsonArray): List<OwnerRef> =
        ownerRefs.mapIndexed { index, element ->
            val obj = element as? JsonObject
                ?: throw ApiException.BadRequest(
                    "ownerRefs[$index] must be an object",
                    details = mapOf("field" to "metadata.ownerRefs"),
                    code = "invalid_owner_reference",
                )
            val kind = obj["kind"]?.jsonPrimitive?.contentOrNull?.trim().orEmpty()
            val id = obj["id"]?.jsonPrimitive?.contentOrNull?.trim().orEmpty()
            if (kind.isEmpty() || id.isEmpty()) {
                throw ApiException.BadRequest(
                    "ownerRefs[$index] requires kind and id",
                    details = mapOf("field" to "metadata.ownerRefs"),
                    code = "invalid_owner_reference",
                )
            }
            OwnerRef(
                kind = kind,
                id = id,
                controller = obj["controller"]?.jsonPrimitive?.booleanOrNull,
                name = obj["name"]?.jsonPrimitive?.contentOrNull,
            )
        }

    fun encode(refs: List<OwnerRef>): JsonArray =
        JsonArray(
            refs.map { ref ->
                JsonObject(
                    buildMap {
                        put("kind", JsonPrimitive(ref.kind))
                        put("id", JsonPrimitive(ref.id))
                        ref.controller?.let { put("controller", JsonPrimitive(it)) }
                        ref.name?.let { put("name", JsonPrimitive(it)) }
                    },
                )
            },
        )

    /**
     * Ensures every owner exists, lives in the same or wider scope than [subject],
     * and that adding these refs would not create an ownership cycle.
     */
    fun validate(
        subject: ResourceRow,
        ownerRefs: JsonArray,
        findById: (String) -> ResourceRow?,
    ) {
        val refs = parse(ownerRefs)
        for (ref in refs) {
            if (ref.id == subject.id) {
                throw ApiException.BadRequest(
                    "owner reference cycle detected",
                    details = mapOf("resourceId" to subject.id, "ownerId" to ref.id),
                    code = "owner_reference_cycle",
                )
            }
            val owner = findById(ref.id)
                ?: throw ApiException.BadRequest(
                    "owner reference target not found",
                    details = mapOf("ownerId" to ref.id, "ownerKind" to ref.kind),
                    code = "invalid_owner_reference",
                )
            if (owner.kind != ref.kind) {
                throw ApiException.BadRequest(
                    "owner reference kind mismatch",
                    details = mapOf(
                        "ownerId" to ref.id,
                        "expectedKind" to ref.kind,
                        "actualKind" to owner.kind,
                    ),
                    code = "invalid_owner_reference",
                )
            }
            if (!isSameOrWiderScope(owner = owner, subject = subject)) {
                throw ApiException.BadRequest(
                    "owner must be in the same or wider scope than the subject",
                    details = mapOf("ownerId" to ref.id, "resourceId" to subject.id),
                    code = "invalid_owner_reference",
                )
            }
            if (wouldCreateCycle(subjectId = subject.id, ownerId = owner.id, findById = findById)) {
                throw ApiException.BadRequest(
                    "owner reference cycle detected",
                    details = mapOf("resourceId" to subject.id, "ownerId" to ref.id),
                    code = "owner_reference_cycle",
                )
            }
        }
    }

    /** Owner is cluster/project/environment at equal or broader nesting than subject. */
    fun isSameOrWiderScope(owner: ResourceRow, subject: ResourceRow): Boolean {
        if (owner.organization != subject.organization) return false
        // Cluster owner (no project): always wider/equal for any subject in the org.
        if (owner.project == null) {
            return owner.environment == null
        }
        // Project owner: subject must share project; owner must not be environment-scoped.
        if (owner.environment == null) {
            return subject.project == owner.project
        }
        // Environment owner: must match subject's project + environment exactly.
        return subject.project == owner.project && subject.environment == owner.environment
    }

    private fun wouldCreateCycle(
        subjectId: String,
        ownerId: String,
        findById: (String) -> ResourceRow?,
    ): Boolean {
        val seen = linkedSetOf<String>()
        var currentId: String? = ownerId
        while (currentId != null) {
            if (currentId == subjectId) return true
            if (!seen.add(currentId)) return true
            val current = findById(currentId) ?: break
            val parents = parse(current.ownerRefs)
            currentId = parents.firstOrNull()?.id
        }
        return false
    }
}

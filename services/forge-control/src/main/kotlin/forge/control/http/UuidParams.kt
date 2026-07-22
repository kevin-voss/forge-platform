package forge.control.http

import io.ktor.http.Parameters
import java.util.UUID

internal fun Parameters.requireUuid(name: String): UUID {
    val raw = this[name]
        ?: throw ApiException.BadRequest("$name is required", mapOf("field" to name))
    return try {
        UUID.fromString(raw)
    } catch (_: IllegalArgumentException) {
        throw ApiException.BadRequest("invalid UUID for $name", mapOf("field" to name))
    }
}

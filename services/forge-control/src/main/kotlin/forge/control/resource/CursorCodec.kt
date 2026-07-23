package forge.control.resource

import forge.control.http.ApiException
import java.util.Base64
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

/**
 * Opaque base64 cursor over `(name, id)` for stable list pagination.
 * Not a security boundary — plain encoding of the sort key.
 */
object CursorCodec {
    private val json = Json { ignoreUnknownKeys = true }

    @Serializable
    data class Cursor(val name: String, val id: String)

    fun encode(name: String, id: String): String {
        val payload = json.encodeToString(Cursor.serializer(), Cursor(name = name, id = id))
        return Base64.getUrlEncoder().withoutPadding().encodeToString(payload.toByteArray(Charsets.UTF_8))
    }

    fun decode(raw: String?): Cursor? {
        if (raw.isNullOrBlank()) return null
        return try {
            val bytes = Base64.getUrlDecoder().decode(raw)
            val cursor = json.decodeFromString(Cursor.serializer(), bytes.toString(Charsets.UTF_8))
            if (cursor.name.isBlank() || cursor.id.isBlank()) {
                throw invalid(raw)
            }
            cursor
        } catch (e: ApiException) {
            throw e
        } catch (_: Exception) {
            throw invalid(raw)
        }
    }

    private fun invalid(raw: String): Nothing =
        throw ApiException.BadRequest(
            "invalid list cursor",
            details = mapOf("cursor" to raw),
            code = "invalid_cursor",
        )
}

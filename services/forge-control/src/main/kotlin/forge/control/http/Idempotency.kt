package forge.control.http

import forge.control.repo.IdempotencyRecord
import forge.control.repo.IdempotencyStore
import io.ktor.http.HttpStatusCode
import io.ktor.server.application.ApplicationCall
import io.ktor.server.response.respond
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import java.security.MessageDigest
import java.util.UUID

suspend fun ApplicationCall.idempotentCreate(
    store: IdempotencyStore?,
    resourceType: String,
    requestBody: String,
    create: () -> Pair<UUID, JsonElement>,
) {
    val key = request.headers["Idempotency-Key"] ?: run {
        val (_, body) = create()
        respond(HttpStatusCode.Created, body)
        return
    }
    if (!key.matches(Regex("""[A-Za-z0-9._-]{1,128}"""))) {
        throw ApiException.BadRequest("Idempotency-Key must be 1-128 URL-safe characters")
    }
    val hash = sha256(requestBody)
    val existing = store?.find(key)
    if (existing != null) {
        if (existing.requestHash != hash || existing.resourceType != resourceType) {
            throw ApiException.Conflict("Idempotency-Key was already used with a different request", code = "idempotency_key_conflict")
        }
        respond(HttpStatusCode.fromValue(existing.responseStatus), Json.parseToJsonElement(existing.responseBody))
        return
    }
    val (id, body) = create()
    store?.save(IdempotencyRecord(key, hash, resourceType, id, HttpStatusCode.Created.value, body.toString()))
    respond(HttpStatusCode.Created, body)
}

private fun sha256(value: String): String =
    MessageDigest.getInstance("SHA-256").digest(value.toByteArray())
        .joinToString("") { "%02x".format(it) }

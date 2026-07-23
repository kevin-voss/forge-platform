package forge.control.manageddb

import java.net.URI
import java.net.URLEncoder
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Duration
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

/** Store generated DB credentials in Forge Secrets; return an opaque reference. */
interface ManagedDbSecretsClient {
    fun putSecret(projectId: UUID, secretName: String, value: String): String
}

/**
 * In-memory store for FakeProvisioner / tests when Secrets is disabled.
 * Values are process-local and never written to Control's database.
 */
class InMemoryManagedDbSecretsClient : ManagedDbSecretsClient {
    private val values = ConcurrentHashMap<String, String>()

    override fun putSecret(projectId: UUID, secretName: String, value: String): String {
        val ref = secretRef(projectId, secretName)
        values[ref] = value
        return ref
    }

    fun get(secretRef: String): String? = values[secretRef]

    companion object {
        fun secretRef(projectId: UUID, secretName: String): String =
            "secret:project/$projectId/env/managed-db/name/$secretName"
    }
}

/**
 * HTTP client for `PUT .../secrets/{name}`.
 * Never logs secret values.
 */
class HttpManagedDbSecretsClient(
    private val secretsUrl: String,
    private val serviceAccountToken: String,
    private val environment: String = "managed-db",
    private val httpClient: HttpClient = HttpClient.newBuilder()
        .connectTimeout(Duration.ofSeconds(2))
        .build(),
    private val json: Json = Json { ignoreUnknownKeys = true },
) : ManagedDbSecretsClient {
    override fun putSecret(projectId: UUID, secretName: String, value: String): String {
        val base = secretsUrl.trimEnd('/')
        val path = "/v1/projects/${enc(projectId.toString())}/envs/${enc(environment)}/secrets/${enc(secretName)}"
        val body = json.encodeToString(SetSecretBody.serializer(), SetSecretBody(value = value))
        val builder = HttpRequest.newBuilder()
            .uri(URI.create("$base$path"))
            .timeout(Duration.ofSeconds(10))
            .header("content-type", "application/json")
            .PUT(HttpRequest.BodyPublishers.ofString(body))
        if (serviceAccountToken.isNotBlank()) {
            builder.header("Authorization", "Bearer $serviceAccountToken")
        }
        forge.control.observability.Otel.inject(builder)
        val response = try {
            httpClient.send(builder.build(), HttpResponse.BodyHandlers.ofString())
        } catch (e: Exception) {
            throw ManagedDbSecretsException(
                "secrets unreachable at $base: ${e.message ?: e.javaClass.simpleName}",
            )
        }
        if (response.statusCode() !in 200..299) {
            throw ManagedDbSecretsException(
                "secrets put HTTP ${response.statusCode()}: ${response.body().take(200)}",
            )
        }
        return InMemoryManagedDbSecretsClient.secretRef(projectId, secretName)
    }

    private fun enc(value: String): String =
        URLEncoder.encode(value, Charsets.UTF_8).replace("+", "%20")
}

@Serializable
private data class SetSecretBody(val value: String)

class ManagedDbSecretsException(message: String) : RuntimeException(message)

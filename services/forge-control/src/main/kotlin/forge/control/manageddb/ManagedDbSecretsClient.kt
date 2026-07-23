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

/** Store/reveal managed-DB secrets in Forge Secrets; Control keeps only refs. */
interface ManagedDbSecretsClient {
    fun putSecret(projectId: UUID, secretName: String, value: String): String
    fun getSecret(secretRef: String): String?
    fun deleteSecret(secretRef: String)
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

    override fun getSecret(secretRef: String): String? = values[secretRef]

    override fun deleteSecret(secretRef: String) {
        values.remove(secretRef)
    }

    fun get(secretRef: String): String? = getSecret(secretRef)

    companion object {
        fun secretRef(projectId: UUID, secretName: String, environment: String = "managed-db"): String =
            "secret:project/$projectId/env/$environment/name/$secretName"
    }
}

/**
 * HTTP client for Secrets put / :access / delete.
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
        val path = secretPath(projectId.toString(), environment, secretName)
        val body = json.encodeToString(SetSecretBody.serializer(), SetSecretBody(value = value))
        val response = send(
            method = "PUT",
            path = path,
            body = body,
        )
        if (response.statusCode() !in 200..299) {
            throw ManagedDbSecretsException(
                "secrets put HTTP ${response.statusCode()}: ${response.body().take(200)}",
            )
        }
        return InMemoryManagedDbSecretsClient.secretRef(projectId, secretName, environment)
    }

    override fun getSecret(secretRef: String): String? {
        val parsed = parseSecretRef(secretRef) ?: return null
        val path = "${secretPath(parsed.projectId, parsed.environment, parsed.name)}:access"
        val response = send(method = "POST", path = path, body = null)
        return when (response.statusCode()) {
            in 200..299 -> {
                val body = json.decodeFromString(AccessSecretBody.serializer(), response.body())
                body.value
            }
            404 -> null
            else -> throw ManagedDbSecretsException(
                "secrets access HTTP ${response.statusCode()}: ${response.body().take(200)}",
            )
        }
    }

    override fun deleteSecret(secretRef: String) {
        val parsed = parseSecretRef(secretRef) ?: return
        val path = secretPath(parsed.projectId, parsed.environment, parsed.name)
        val response = send(method = "DELETE", path = path, body = null)
        if (response.statusCode() !in 200..299 && response.statusCode() != 404) {
            throw ManagedDbSecretsException(
                "secrets delete HTTP ${response.statusCode()}: ${response.body().take(200)}",
            )
        }
    }

    private fun secretPath(projectId: String, environment: String, name: String): String =
        "/v1/projects/${enc(projectId)}/envs/${enc(environment)}/secrets/${enc(name)}"

    private fun send(method: String, path: String, body: String?): HttpResponse<String> {
        val base = secretsUrl.trimEnd('/')
        val builder = HttpRequest.newBuilder()
            .uri(URI.create("$base$path"))
            .timeout(Duration.ofSeconds(10))
        when (method) {
            "PUT" -> {
                builder.header("content-type", "application/json")
                builder.PUT(HttpRequest.BodyPublishers.ofString(body.orEmpty()))
            }
            "POST" -> builder.POST(HttpRequest.BodyPublishers.noBody())
            "DELETE" -> builder.DELETE()
            else -> throw IllegalArgumentException("unsupported method $method")
        }
        if (serviceAccountToken.isNotBlank()) {
            builder.header("Authorization", "Bearer $serviceAccountToken")
        }
        forge.control.observability.Otel.inject(builder)
        return try {
            httpClient.send(builder.build(), HttpResponse.BodyHandlers.ofString())
        } catch (e: Exception) {
            throw ManagedDbSecretsException(
                "secrets unreachable at $base: ${e.message ?: e.javaClass.simpleName}",
            )
        }
    }

    private fun enc(value: String): String =
        URLEncoder.encode(value, Charsets.UTF_8).replace("+", "%20")

    companion object {
        private val REF =
            Regex("^secret:project/([^/]+)/env/([^/]+)/name/(.+)$")

        fun parseSecretRef(secretRef: String): ParsedSecretRef? {
            val m = REF.matchEntire(secretRef.trim()) ?: return null
            return ParsedSecretRef(
                projectId = m.groupValues[1],
                environment = m.groupValues[2],
                name = m.groupValues[3],
            )
        }
    }
}

data class ParsedSecretRef(
    val projectId: String,
    val environment: String,
    val name: String,
)

@Serializable
private data class SetSecretBody(val value: String)

@Serializable
private data class AccessSecretBody(val value: String = "")

class ManagedDbSecretsException(message: String) : RuntimeException(message)

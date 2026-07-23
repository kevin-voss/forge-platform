package forge.control.reconcile

import java.net.URI
import java.net.URLEncoder
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Duration
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

/** Resolved env bundle from forge-secrets (transient — never persist values). */
data class ResolvedEnvBundle(
    val env: Map<String, String>,
    val versionFingerprint: String,
)

sealed class SecretsResolveResult {
    data class Ok(val bundle: ResolvedEnvBundle) : SecretsResolveResult()
    data class Missing(val detail: String) : SecretsResolveResult()
    data class Unavailable(val detail: String) : SecretsResolveResult()
    data class Failed(val detail: String) : SecretsResolveResult()
}

/** Seam for Control → Secrets resolve at deploy time. */
interface SecretsClient {
    fun resolve(projectId: String, environment: String, service: String): SecretsResolveResult
}

/** No-op client when Secrets is not configured — empty bundle, no injection. */
object NoOpSecretsClient : SecretsClient {
    override fun resolve(projectId: String, environment: String, service: String): SecretsResolveResult =
        SecretsResolveResult.Ok(ResolvedEnvBundle(env = emptyMap(), versionFingerprint = ""))
}

/**
 * HTTP client for `POST .../services/{svc}/resolve`.
 * Uses a service-account bearer token; never logs env values.
 */
class HttpSecretsClient(
    private val secretsUrl: String,
    private val serviceAccountToken: String,
    private val httpClient: HttpClient = HttpClient.newBuilder()
        .connectTimeout(Duration.ofSeconds(2))
        .build(),
    private val json: Json = Json { ignoreUnknownKeys = true },
) : SecretsClient {
    override fun resolve(projectId: String, environment: String, service: String): SecretsResolveResult {
        if (projectId.isBlank() || environment.isBlank() || service.isBlank()) {
            return SecretsResolveResult.Ok(ResolvedEnvBundle(emptyMap(), ""))
        }
        val base = secretsUrl.trimEnd('/')
        val path = "/v1/projects/${enc(projectId)}/envs/${enc(environment)}/services/${enc(service)}/resolve"
        val builder = HttpRequest.newBuilder()
            .uri(URI.create("$base$path"))
            .timeout(Duration.ofSeconds(10))
            .header("content-type", "application/json")
            .POST(HttpRequest.BodyPublishers.noBody())
        if (serviceAccountToken.isNotBlank()) {
            builder.header("Authorization", "Bearer $serviceAccountToken")
        }
        val response = try {
            httpClient.send(builder.build(), HttpResponse.BodyHandlers.ofString())
        } catch (e: Exception) {
            return SecretsResolveResult.Unavailable(
                "secrets unreachable at $base: ${e.message ?: e.javaClass.simpleName}",
            )
        }
        return when (response.statusCode()) {
            in 200..299 -> {
                val body = try {
                    json.decodeFromString(ResolveBody.serializer(), response.body())
                } catch (e: Exception) {
                    return SecretsResolveResult.Failed(
                        "secrets resolve decode failed: ${e.message ?: e.javaClass.simpleName}",
                    )
                }
                SecretsResolveResult.Ok(
                    ResolvedEnvBundle(
                        env = body.env,
                        versionFingerprint = body.version_fingerprint,
                    ),
                )
            }
            422 -> SecretsResolveResult.Missing(
                "missing bound secrets/config: ${response.body().take(200)}",
            )
            in 500..599 -> SecretsResolveResult.Unavailable(
                "secrets HTTP ${response.statusCode()}",
            )
            else -> SecretsResolveResult.Failed(
                "secrets resolve HTTP ${response.statusCode()}: ${response.body().take(200)}",
            )
        }
    }

    private fun enc(value: String): String =
        URLEncoder.encode(value, Charsets.UTF_8).replace("+", "%20")
}

@Serializable
private data class ResolveBody(
    val env: Map<String, String> = emptyMap(),
    val version_fingerprint: String = "",
)

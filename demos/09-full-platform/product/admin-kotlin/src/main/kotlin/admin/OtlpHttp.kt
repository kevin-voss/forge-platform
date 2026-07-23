package admin

import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.security.SecureRandom
import java.time.Duration
import java.util.Locale

/** Minimal OTLP/HTTP JSON span exporter (no OpenTelemetry SDK dependency). */
object OtlpHttp {
    private val random = SecureRandom()
    private val client: HttpClient = HttpClient.newBuilder()
        .connectTimeout(Duration.ofSeconds(2))
        .build()

    fun enabled(env: Map<String, String> = System.getenv()): Boolean {
        val raw = env["FORGE_OTEL_ENABLED"]?.trim()?.lowercase(Locale.ROOT).orEmpty()
        return raw in setOf("1", "true", "yes", "on")
    }

    fun endpoint(env: Map<String, String> = System.getenv()): String {
        var ep = env["FORGE_OTEL_EXPORTER_ENDPOINT"]?.trim().orEmpty()
        if (ep.isEmpty()) {
            ep = env["OTEL_EXPORTER_OTLP_ENDPOINT"]?.trim().orEmpty()
        }
        if (ep.isEmpty()) {
            ep = "http://host.docker.internal:4318"
        }
        if (ep.endsWith(":4317")) {
            ep = ep.removeSuffix(":4317") + ":4318"
        }
        ep = ep.trimEnd('/')
        if (!ep.endsWith("/v1/traces")) {
            ep += "/v1/traces"
        }
        return ep
    }

    fun exportSpan(
        serviceName: String,
        spanName: String,
        traceparent: String?,
        statusCode: Int,
        path: String,
        env: Map<String, String> = System.getenv(),
    ) {
        if (!enabled(env)) return
        val (traceId, parentId) = parseTraceparent(traceparent)
        val spanId = hex(8)
        val nowNs = System.currentTimeMillis() * 1_000_000L
        val startNs = nowNs - 5_000_000L
        val body = """
            {"resourceSpans":[{"resource":{"attributes":[
              {"key":"service.name","value":{"stringValue":"$serviceName"}},
              {"key":"forge.service","value":{"stringValue":"$serviceName"}}
            ]},"scopeSpans":[{"scope":{"name":"incident-admin"},"spans":[{
              "traceId":"$traceId","spanId":"$spanId","parentSpanId":"$parentId",
              "name":"$spanName","kind":2,
              "startTimeUnixNano":"$startNs","endTimeUnixNano":"$nowNs",
              "attributes":[
                {"key":"http.response.status_code","value":{"intValue":$statusCode}},
                {"key":"url.path","value":{"stringValue":"$path"}},
                {"key":"forge.service","value":{"stringValue":"$serviceName"}}
              ]
            }]}]}]}
        """.trimIndent()
        try {
            val req = HttpRequest.newBuilder(URI.create(endpoint(env)))
                .timeout(Duration.ofSeconds(2))
                .header("Content-Type", "application/json")
                .POST(HttpRequest.BodyPublishers.ofString(body))
                .build()
            client.send(req, HttpResponse.BodyHandlers.discarding())
        } catch (_: Exception) {
            // Best-effort export; product stays available if Observe is down.
        }
    }

    private fun parseTraceparent(header: String?): Pair<String, String> {
        if (!header.isNullOrBlank()) {
            val parts = header.trim().split('-')
            if (parts.size >= 3 && parts[1].length == 32 && parts[2].length == 16) {
                return parts[1] to parts[2]
            }
        }
        return hex(16) to hex(8)
    }

    private fun hex(bytes: Int): String {
        val buf = ByteArray(bytes)
        random.nextBytes(buf)
        return buf.joinToString("") { b -> "%02x".format(b) }
    }
}

package forge.control.resource.http

import forge.control.http.ApiException
import forge.control.http.RequestId
import forge.control.logging.JsonLog
import forge.control.resource.KindRegistry
import forge.control.resource.ResourceEvent
import forge.control.resource.ResourceEventRepository
import forge.control.resource.ResourceEnvelopeResponse
import forge.control.resource.ResourceWatchEvent
import forge.control.telemetry.Telemetry
import io.ktor.http.CacheControl
import io.ktor.http.ContentType
import io.ktor.http.HttpStatusCode
import io.ktor.server.application.call
import io.ktor.server.response.cacheControl
import io.ktor.server.response.respondTextWriter
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.route
import io.opentelemetry.api.common.AttributeKey
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.serialization.json.Json
import java.time.Duration
import java.util.concurrent.atomic.AtomicInteger
import kotlin.coroutines.coroutineContext

private val watchJson = Json {
    ignoreUnknownKeys = true
    encodeDefaults = true
    explicitNulls = false
}

/**
 * SSE watch for registered resource plurals.
 *
 * `GET /v1/watch/{plural}?since={resourceVersion}` replays retained events with
 * `resource_version > since`, then live-tails. Stale cursors below the retention
 * floor return `410 Gone` with `resource_version_too_old`.
 */
fun Route.watchRoutes(
    events: ResourceEventRepository,
    kinds: KindRegistry,
    defaultOrganization: String = "default",
    retentionHours: Long = 24,
    heartbeatSeconds: Long = 15,
    maxConnections: Int = 200,
    connectionCounter: AtomicInteger = AtomicInteger(0),
    pollIntervalMs: Long = 500,
    log: JsonLog? = null,
    telemetry: Telemetry = Telemetry.current(),
) {
    route("/v1/watch/{plural}") {
        get {
            val span = telemetry.startSpan("resource.watch")
            val kind = resolveKind(kinds, call.parameters["plural"])
            val requestId = RequestId.from(call)
            try {
                span.setAttribute(AttributeKey.stringKey("kind"), kind.kind)
                val sinceRaw = call.request.queryParameters["since"]
                    ?: throw ApiException.BadRequest(
                        "since is required",
                        details = mapOf("field" to "since"),
                        code = "invalid_request",
                    )
                val since = sinceRaw.toLongOrNull()
                    ?: throw ApiException.BadRequest(
                        "since must be an integer resourceVersion",
                        details = mapOf("field" to "since"),
                        code = "invalid_request",
                    )
                if (since < 0) {
                    throw ApiException.BadRequest(
                        "since must be non-negative",
                        details = mapOf("field" to "since"),
                        code = "invalid_request",
                    )
                }
                span.setAttribute(AttributeKey.longKey("since"), since)

                val retention = Duration.ofHours(retentionHours.coerceAtLeast(1))
                val oldest = events.oldestRetainedVersion(retention)
                if (oldest != null && since + 1 < oldest) {
                    throw ApiException.Gone(
                        "watch cursor is older than the retained event window",
                        details = mapOf(
                            "since" to since.toString(),
                            "oldestRetained" to oldest.toString(),
                        ),
                        code = "resource_version_too_old",
                    )
                }

                while (true) {
                    val current = connectionCounter.get()
                    if (current >= maxConnections) {
                        throw ApiException.ServiceUnavailable(
                            "watch connection limit reached",
                            details = mapOf(
                                "maxConnections" to maxConnections.toString(),
                                "active" to current.toString(),
                            ),
                            code = "watch_connection_limit",
                        )
                    }
                    if (connectionCounter.compareAndSet(current, current + 1)) break
                }

                telemetry.watchConnectionOpened(kind.kind)
                var replayCount = 0
                var cursor = since
                var lastHeartbeatAt = System.nanoTime()
                val heartbeatNanos = heartbeatSeconds.coerceAtLeast(1) * 1_000_000_000L

                try {
                    call.response.cacheControl(CacheControl.NoCache(null))
                    call.respondTextWriter(
                        contentType = ContentType.Text.EventStream,
                        status = HttpStatusCode.OK,
                    ) {
                        // Replay retained history first.
                        while (coroutineContext.isActive) {
                            val batch = events.listAfter(
                                kind = kind.kind,
                                organization = defaultOrganization,
                                since = cursor,
                            )
                            if (batch.isEmpty()) break
                            for (event in batch) {
                                writeSseEvent(event)
                                cursor = event.resourceVersion
                                replayCount++
                            }
                            if (batch.size < 500) break
                        }

                        log?.info(
                            "resource.watch.start",
                            "kind" to kind.kind,
                            "since" to since,
                            "replay_count" to replayCount,
                            "request_id" to requestId,
                        )
                        span.setAttribute(AttributeKey.longKey("replay_count"), replayCount.toLong())

                        // Live tail + heartbeats.
                        while (coroutineContext.isActive) {
                            val batch = events.listAfter(
                                kind = kind.kind,
                                organization = defaultOrganization,
                                since = cursor,
                            )
                            if (batch.isNotEmpty()) {
                                for (event in batch) {
                                    writeSseEvent(event)
                                    cursor = event.resourceVersion
                                }
                                lastHeartbeatAt = System.nanoTime()
                            } else {
                                val now = System.nanoTime()
                                if (now - lastHeartbeatAt >= heartbeatNanos) {
                                    write(": heartbeat\n\n")
                                    flush()
                                    lastHeartbeatAt = now
                                }
                                delay(pollIntervalMs)
                            }
                        }
                    }
                } finally {
                    connectionCounter.decrementAndGet()
                    telemetry.watchConnectionClosed(kind.kind)
                    log?.info(
                        "resource.watch.end",
                        "kind" to kind.kind,
                        "since" to since,
                        "replay_count" to replayCount,
                        "request_id" to requestId,
                        "last_resource_version" to cursor,
                    )
                }
            } finally {
                span.end()
            }
        }
    }
}

private fun java.io.Writer.writeSseEvent(event: ResourceEvent) {
    val resource = watchJson.decodeFromJsonElement(
        ResourceEnvelopeResponse.serializer(),
        event.payload,
    )
    val frame = ResourceWatchEvent(
        type = event.eventType.name,
        resourceVersion = event.resourceVersion.toString(),
        resource = resource,
    )
    val data = watchJson.encodeToString(ResourceWatchEvent.serializer(), frame)
    write("event: ${event.eventType.name}\n")
    write("id: ${event.resourceVersion}\n")
    write("data: $data\n\n")
    flush()
}

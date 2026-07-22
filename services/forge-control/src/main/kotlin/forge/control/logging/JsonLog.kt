package forge.control.logging

import forge.control.telemetry.LoggingContext
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import java.time.Instant
import java.time.ZoneOffset
import java.time.format.DateTimeFormatter

private val LEVEL_RANK = mapOf(
    "debug" to 10,
    "info" to 20,
    "warn" to 30,
    "error" to 40,
)

private val TIMESTAMP_FMT = DateTimeFormatter.ofPattern("yyyy-MM-dd'T'HH:mm:ss'Z'")
    .withZone(ZoneOffset.UTC)

/** Structured JSON logging to stdout (timestamp, level, service, message). */
class JsonLog(
    private val service: String,
    level: String,
) {
    private val min = LEVEL_RANK[level.lowercase()] ?: 20

    fun debug(message: String, vararg fields: Pair<String, Any?>) = emit("debug", message, fields)
    fun info(message: String, vararg fields: Pair<String, Any?>) = emit("info", message, fields)
    fun warn(message: String, vararg fields: Pair<String, Any?>) = emit("warn", message, fields)
    fun error(message: String, vararg fields: Pair<String, Any?>) = emit("error", message, fields)

    private fun emit(level: String, message: String, fields: Array<out Pair<String, Any?>>) {
        if ((LEVEL_RANK[level] ?: 20) < min) return
        val payload = buildJsonObject {
            put("timestamp", JsonPrimitive(TIMESTAMP_FMT.format(Instant.now())))
            put("level", JsonPrimitive(level))
            put("service", JsonPrimitive(service))
            put("message", JsonPrimitive(message))
            val correlation = LoggingContext.current()
            put("requestId", JsonPrimitive(correlation.requestId))
            correlation.traceId?.let { put("traceId", JsonPrimitive(it)) }
            correlation.spanId?.let { put("spanId", JsonPrimitive(it)) }
            for ((key, value) in fields) {
                when (value) {
                    null -> put(key, JsonPrimitive("null"))
                    is Number -> put(key, JsonPrimitive(value))
                    is Boolean -> put(key, JsonPrimitive(value))
                    else -> put(key, JsonPrimitive(value.toString()))
                }
            }
        }
        println(payload.toString())
        System.out.flush()
    }
}

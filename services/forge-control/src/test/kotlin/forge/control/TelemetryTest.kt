package forge.control

import forge.control.http.RequestId
import forge.control.logging.JsonLog
import forge.control.telemetry.Telemetry
import forge.control.telemetry.TelemetryConfig
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull

class TelemetryTest {
    @Test
    fun disabledTelemetryIsNoOp() {
        val telemetry = Telemetry.initialize(
            TelemetryConfig(false, "forge-control", "http://otel-collector:4317"),
        )

        assertFalse(telemetry.enabled)
        telemetry.inSpan("unit-test") { assertFalse(telemetry.startSpan("nested").spanContext.isValid) }
    }

    @Test
    fun jsonLogsContainRequiredPlatformFields() {
        val original = System.out
        val output = java.io.ByteArrayOutputStream()
        try {
            System.setOut(java.io.PrintStream(output))
            RequestId.set("req_test")
            JsonLog("forge-control", "info").info("test event")
        } finally {
            RequestId.clear()
            System.setOut(original)
        }

        val log = Json.parseToJsonElement(output.toString().trim()).jsonObject
        assertNotNull(log["timestamp"])
        assertEquals("info", log["level"]?.jsonPrimitive?.content)
        assertEquals("forge-control", log["service"]?.jsonPrimitive?.content)
        assertEquals("test event", log["message"]?.jsonPrimitive?.content)
        assertEquals("req_test", log["requestId"]?.jsonPrimitive?.content)
    }
}

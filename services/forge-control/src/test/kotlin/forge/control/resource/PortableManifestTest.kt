package forge.control.resource

import forge.control.http.ApiException
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

class PortableManifestTest {
    @Test
    fun acceptsPortableApplicationSpec() {
        PortableManifest.validate(
            "Application",
            buildJsonObject {
                put("image", JsonPrimitive("registry.forge.internal/invoice-api:1.0.0"))
                put(
                    "resources",
                    buildJsonObject {
                        put("cpu", JsonPrimitive("1000m"))
                        put("memory", JsonPrimitive("1024Mi"))
                    },
                )
            },
        )
    }

    @Test
    fun rejectsProviderField() {
        val err = assertFailsWith<ApiException.BadRequest> {
            PortableManifest.validate(
                "Application",
                buildJsonObject {
                    put("provider", JsonPrimitive("aws"))
                    put("image", JsonPrimitive("registry.forge.internal/invoice-api:1.0.0"))
                },
            )
        }
        assertEquals("portable_manifest_violation", err.code)
    }

    @Test
    fun rejectsMachineTypeValue() {
        val err = assertFailsWith<ApiException.BadRequest> {
            PortableManifest.validate(
                "Application",
                buildJsonObject {
                    put("size", JsonPrimitive("t3.large"))
                },
            )
        }
        assertEquals("portable_manifest_violation", err.code)
    }
}

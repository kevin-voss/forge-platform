package forge.control.resource.http

import forge.control.resource.KindRegistry
import forge.control.resource.ResourceScope
import io.ktor.client.call.body
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.client.request.get
import io.ktor.client.request.post
import io.ktor.client.request.setBody
import io.ktor.http.ContentType
import io.ktor.http.HttpStatusCode
import io.ktor.http.contentType
import io.ktor.serialization.kotlinx.json.json
import io.ktor.server.application.install
import io.ktor.server.plugins.contentnegotiation.ContentNegotiation as ServerContentNegotiation
import io.ktor.server.routing.routing
import io.ktor.server.testing.testApplication
import kotlinx.serialization.json.Json
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class KindRoutesTest {
    private val json = Json { ignoreUnknownKeys = true; encodeDefaults = true; explicitNulls = false }

    @Test
    fun postCreatesAndGetListsKinds() = testApplication {
        val registry = KindRegistry()
        application {
            install(ServerContentNegotiation) {
                json(json)
            }
            routing {
                kindRoutes(registry)
            }
        }
        val client = createClient {
            install(ContentNegotiation) { json(json) }
        }

        val created = client.post("/v1/kinds") {
            contentType(ContentType.Application.Json)
            setBody(
                KindRegistrationRequest(
                    kind = "Endpoint",
                    plural = "endpoints",
                    scope = "namespaced",
                    controller = "forge-discovery",
                    schemaVersion = 1,
                    idPrefix = "end",
                ),
            )
        }
        assertEquals(HttpStatusCode.Created, created.status)
        val body = created.body<KindDescriptorResponse>()
        assertEquals("Endpoint", body.kind)
        assertEquals("endpoints", body.plural)
        assertEquals("environment", body.scope)
        assertEquals("forge-discovery", body.controller)

        val again = client.post("/v1/kinds") {
            contentType(ContentType.Application.Json)
            setBody(
                KindRegistrationRequest(
                    kind = "Endpoint",
                    plural = "endpoints",
                    scope = "namespaced",
                    controller = "forge-discovery",
                    schemaVersion = 1,
                    idPrefix = "end",
                ),
            )
        }
        assertEquals(HttpStatusCode.OK, again.status)

        val listed = client.get("/v1/kinds").body<List<KindDescriptorResponse>>()
        assertTrue(listed.any { it.plural == "endpoints" && it.controller == "forge-discovery" })
        assertEquals(ResourceScope.Environment, registry.get("Endpoint")?.scope)
    }
}

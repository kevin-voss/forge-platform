package forge.control.auth

import forge.control.config.AppConfig
import forge.control.config.DatabaseConfig
import forge.control.config.loadAppConfig
import forge.control.http.AlwaysHealthyDb
import forge.control.http.ApiException
import forge.control.http.ErrorEnvelope
import forge.control.http.Readiness
import forge.control.forgeControlModule
import io.ktor.client.call.body
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.client.request.get
import io.ktor.client.request.header
import io.ktor.client.request.post
import io.ktor.client.request.setBody
import io.ktor.http.ContentType
import io.ktor.http.HttpStatusCode
import io.ktor.http.contentType
import io.ktor.serialization.kotlinx.json.json
import io.ktor.server.testing.testApplication
import kotlinx.serialization.json.Json
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertTrue

class AuthMiddlewareTest {
    private val projectId = "11111111-1111-1111-1111-111111111111"
    private val serviceId = "22222222-2222-2222-2222-222222222222"

    private val scope = MapProjectScopeResolver(
        mapOf(
            ScopeKind.Project to mapOf(projectId to projectId),
            ScopeKind.Service to mapOf(serviceId to projectId),
        ),
    )
    private val routes = RouteActionMap(scope)

    @Test
    fun noTokenReturns401() {
        val identity = FakeIdentityClient()
        val mw = AuthMiddleware("enforce", identity, routes)
        val ex = assertFailsWith<ApiException.Unauthorized> {
            mw.enforce("POST", "/v1/services/$serviceId/deployments", null)
        }
        assertEquals("unauthenticated", ex.code)
        assertEquals(1, mw.metrics.unauthorized.get())
    }

    @Test
    fun inactiveTokenReturns401() {
        val identity = FakeIdentityClient()
        identity.stubIntrospect("dead", IntrospectResult(active = false))
        val mw = AuthMiddleware("enforce", identity, routes)
        val ex = assertFailsWith<ApiException.Unauthorized> {
            mw.enforce("GET", "/v1/projects/$projectId", "Bearer dead")
        }
        assertEquals("unauthenticated", ex.code)
    }

    @Test
    fun routeActionMapMapsMutationRoutes() {
        val create = routes.resolve("POST", "/v1/services/$serviceId/deployments")
        assertIs<AuthTarget.Authorize>(create)
        assertEquals("deployment.create", create.action)
        assertEquals(projectId, create.projectId)

        val update = routes.resolve("PATCH", "/v1/deployments/33333333-3333-3333-3333-333333333333")
        // Without deployment scope mapping → authenticate-only fallback
        assertIs<AuthTarget.AuthenticateOnly>(update)

        val mappedUpdate = RouteActionMap(
            MapProjectScopeResolver(
                mapOf(ScopeKind.Deployment to mapOf("33333333-3333-3333-3333-333333333333" to projectId)),
            ),
        ).resolve("PATCH", "/v1/deployments/33333333-3333-3333-3333-333333333333")
        assertIs<AuthTarget.Authorize>(mappedUpdate)
        assertEquals("deployment.update", mappedUpdate.action)

        val envWrite = routes.resolve("POST", "/v1/projects/$projectId/environments")
        assertIs<AuthTarget.Authorize>(envWrite)
        assertEquals("environment.write", envWrite.action)

        val projectRead = routes.resolve("GET", "/v1/projects/$projectId")
        assertIs<AuthTarget.Authorize>(projectRead)
        assertEquals("project.read", projectRead.action)

        assertIs<AuthTarget.AuthenticateOnly>(
            routes.resolve("POST", "/v1/databases/instances"),
        )
        val dbInstanceId = "44444444-4444-4444-4444-444444444444"
        val dbReadUnmapped = routes.resolve("GET", "/v1/databases/instances/$dbInstanceId")
        assertIs<AuthTarget.AuthenticateOnly>(dbReadUnmapped)
        val dbReadMapped = RouteActionMap(
            MapProjectScopeResolver(
                mapOf(ScopeKind.DbInstance to mapOf(dbInstanceId to projectId)),
            ),
        ).resolve("GET", "/v1/databases/instances/$dbInstanceId")
        assertIs<AuthTarget.Authorize>(dbReadMapped)
        assertEquals("database.read", dbReadMapped.action)

        assertIs<AuthTarget.Skip>(routes.resolve("GET", "/health/live"))
        assertIs<AuthTarget.Skip>(routes.resolve("POST", "/v1/nodes/register"))
    }

    @Test
    fun allowedRoleProceedsDisallowedReturns403() {
        val identity = FakeIdentityClient()
        identity.stubIntrospect(
            "dev-token",
            IntrospectResult(
                active = true,
                principal_type = "user",
                principal_id = "usr-dev",
            ),
        )
        identity.stubIntrospect(
            "viewer-token",
            IntrospectResult(
                active = true,
                principal_type = "user",
                principal_id = "usr-view",
            ),
        )
        identity.stubAuthz(
            "user",
            "usr-dev",
            projectId,
            "deployment.create",
            AuthzDecision(allow = true, role = "developer", reason = "ok"),
        )
        identity.stubAuthz(
            "user",
            "usr-view",
            projectId,
            "deployment.create",
            AuthzDecision(allow = false, role = "viewer", reason = "viewer may not deployment.create"),
        )

        val mw = AuthMiddleware("enforce", identity, routes)
        val allowed = mw.enforce(
            "POST",
            "/v1/services/$serviceId/deployments",
            "Bearer dev-token",
        )
        assertEquals("usr-dev", allowed?.id)

        val denied = assertFailsWith<ApiException.Forbidden> {
            mw.enforce(
                "POST",
                "/v1/services/$serviceId/deployments",
                "Bearer viewer-token",
            )
        }
        assertEquals("forbidden", denied.code)
        assertEquals("deployment.create", denied.details?.get("required_action"))
        assertEquals("viewer", denied.details?.get("role"))
    }

    @Test
    fun identityUnreachableFailsClosedWith503() {
        val identity = FakeIdentityClient(unreachable = true)
        val mw = AuthMiddleware("enforce", identity, routes)
        val ex = assertFailsWith<ApiException.ServiceUnavailable> {
            mw.enforce("POST", "/v1/services/$serviceId/deployments", "Bearer any")
        }
        assertEquals("service_unavailable", ex.code)
    }

    @Test
    fun devModeBypassesAuth() {
        val identity = FakeIdentityClient()
        val mw = AuthMiddleware("dev", identity, routes)
        assertTrue(mw.isDevBypass)
        val principal = mw.enforce("POST", "/v1/services/$serviceId/deployments", null)
        assertEquals("dev", principal?.id)
        assertEquals(0, identity.introspectCalls)
    }

    @Test
    fun defaultAuthModeIsEnforce() {
        val cfg = loadAppConfig(mapOf("PORT" to "4001"))
        assertEquals("enforce", cfg.authMode)
        assertEquals("http://forge-identity:4002", cfg.identityUrl)
        assertEquals(10L, cfg.introspectCacheTtlS)
        assertEquals(10L, cfg.authzCacheTtlS)
        assertFalse(AuthMiddleware(cfg.authMode, FakeIdentityClient(), routes).isDevBypass)
    }

    @Test
    fun httpNoTokenReturns401Envelope() = testApplication {
        val identity = FakeIdentityClient()
        val mw = AuthMiddleware("enforce", identity, routes)
        application {
            forgeControlModule(
                cfg = testCfg(authMode = "enforce"),
                readiness = Readiness(initial = true),
                dbProbe = AlwaysHealthyDb,
                authMiddleware = mw,
            )
        }
        val client = createClient {
            install(ContentNegotiation) {
                json(Json { ignoreUnknownKeys = true })
            }
        }
        val response = client.post("/v1/projects") {
            contentType(ContentType.Application.Json)
            setBody("""{"name":"x"}""")
        }
        assertEquals(HttpStatusCode.Unauthorized, response.status)
        val body = response.body<ErrorEnvelope>()
        assertEquals("unauthenticated", body.error.code)
    }

    @Test
    fun httpHealthSkipsAuth() = testApplication {
        val mw = AuthMiddleware("enforce", FakeIdentityClient(), routes)
        application {
            forgeControlModule(
                cfg = testCfg(authMode = "enforce"),
                readiness = Readiness(initial = true),
                dbProbe = AlwaysHealthyDb,
                authMiddleware = mw,
            )
        }
        val client = createClient {
            install(ContentNegotiation) {
                json(Json { ignoreUnknownKeys = true })
            }
        }
        val response = client.get("/health/live")
        assertEquals(HttpStatusCode.OK, response.status)
    }

    @Test
    fun httpViewerDeniedReturns403Envelope() = testApplication {
        val identity = FakeIdentityClient()
        identity.stubIntrospect(
            "viewer",
            IntrospectResult(active = true, principal_type = "user", principal_id = "usr-view"),
        )
        identity.stubAuthz(
            "user",
            "usr-view",
            projectId,
            "environment.write",
            AuthzDecision(allow = false, role = "viewer", reason = "denied"),
        )
        val mw = AuthMiddleware("enforce", identity, routes)
        application {
            forgeControlModule(
                cfg = testCfg(authMode = "enforce"),
                readiness = Readiness(initial = true),
                dbProbe = AlwaysHealthyDb,
                authMiddleware = mw,
            )
        }
        val client = createClient {
            install(ContentNegotiation) {
                json(Json { ignoreUnknownKeys = true })
            }
        }
        val response = client.post("/v1/projects/$projectId/environments") {
            header("Authorization", "Bearer viewer")
            contentType(ContentType.Application.Json)
            setBody("""{"name":"dev"}""")
        }
        assertEquals(HttpStatusCode.Forbidden, response.status)
        val body = response.body<ErrorEnvelope>()
        assertEquals("forbidden", body.error.code)
        assertEquals("environment.write", body.error.details?.get("required_action"))
        assertEquals("viewer", body.error.details?.get("role"))
    }

    @Test
    fun introspectCacheHonorsTtlForRevocation() {
        var now = 1_000L
        val cache = IntrospectionCache(ttlMillis = 10_000, clock = { now })
        cache.put("tok", IntrospectResult(active = true, principal_type = "user", principal_id = "u1"))
        assertTrue(cache.get("tok")!!.active)
        now = 12_000L
        assertEquals(null, cache.get("tok"))
    }

    private fun testCfg(authMode: String) = AppConfig(
        port = 8080,
        serviceName = "forge-control",
        serviceVersion = "0.1.0",
        logLevel = "info",
        otelEnabled = false,
        otlpEndpoint = "http://otel-collector:4317",
        env = "test",
        authMode = authMode,
        shutdownGraceSeconds = 10,
        database = DatabaseConfig(
            url = "jdbc:postgresql://127.0.0.1:5001/forge",
            user = "forge",
            password = "forge",
            schema = "control",
            poolMax = 10,
            migrateOnStart = true,
        ),
    )
}

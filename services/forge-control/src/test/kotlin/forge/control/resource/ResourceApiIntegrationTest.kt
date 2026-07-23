package forge.control.resource

import forge.control.ControlServices
import forge.control.buildKindRegistry
import forge.control.config.AppConfig
import forge.control.config.DatabaseConfig
import forge.control.db.Db
import forge.control.forgeControlModule
import forge.control.http.ErrorEnvelope
import forge.control.http.Readiness
import forge.control.repo.JdbcApplicationRepository
import forge.control.repo.JdbcAuditRepository
import forge.control.repo.JdbcDeploymentRepository
import forge.control.repo.JdbcEnvironmentRepository
import forge.control.repo.JdbcIdempotencyStore
import forge.control.repo.JdbcProjectRepository
import forge.control.repo.JdbcServiceRepository
import forge.control.resource.http.ListEnvelope
import forge.control.service.ApplicationService
import forge.control.service.DeploymentService
import forge.control.service.EnvironmentService
import forge.control.service.ProjectService
import forge.control.service.ProjectTreeService
import forge.control.service.RelationshipValidator
import forge.control.service.ServiceService
import io.ktor.client.call.body
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.client.request.accept
import io.ktor.client.request.delete
import io.ktor.client.request.get
import io.ktor.client.request.header
import io.ktor.client.request.patch
import io.ktor.client.request.post
import io.ktor.client.request.put
import io.ktor.client.request.setBody
import io.ktor.http.ContentType
import io.ktor.http.HttpStatusCode
import io.ktor.http.contentType
import io.ktor.serialization.kotlinx.json.json
import io.ktor.server.testing.ApplicationTestBuilder
import io.ktor.server.testing.testApplication
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.jupiter.api.AfterAll
import org.junit.jupiter.api.Assumptions.assumeTrue
import org.junit.jupiter.api.BeforeAll
import org.junit.jupiter.api.MethodOrderer
import org.junit.jupiter.api.Order
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.TestInstance
import org.junit.jupiter.api.TestMethodOrder
import java.sql.DriverManager
import java.util.UUID
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

/**
 * Generic resource CRUD + concurrency against foundation Postgres.
 * Skipped when the DB is unreachable. Excluded from default `test` task.
 */
@TestInstance(TestInstance.Lifecycle.PER_CLASS)
@TestMethodOrder(MethodOrderer.OrderAnnotation::class)
class ResourceApiIntegrationTest {
    private val jdbcUrl = System.getenv("DATABASE_URL")
        ?: "jdbc:postgresql://127.0.0.1:5001/forge"
    private val dbUser = System.getenv("DATABASE_USER") ?: "forge"
    private val dbPassword = System.getenv("DATABASE_PASSWORD") ?: "forge"

    private lateinit var db: Db
    private lateinit var services: ControlServices
    private val cfg = AppConfig(
        port = 8080,
        serviceName = "forge-control",
        serviceVersion = "0.1.0",
        logLevel = "info",
        otelEnabled = false,
        otlpEndpoint = "http://otel-collector:4317",
        env = "test",
        authMode = "dev",
        shutdownGraceSeconds = 10,
        database = DatabaseConfig(
            url = jdbcUrl,
            user = dbUser,
            password = dbPassword,
            schema = "control",
            poolMax = 4,
            migrateOnStart = true,
        ),
        resourceApiEnabled = true,
    )

    private val json = Json { ignoreUnknownKeys = true; encodeDefaults = true; explicitNulls = false }
    private val project = "invoice-platform-${UUID.randomUUID().toString().take(8)}"
    private val environment = "dev"
    private var widgetName = "sample"

    @BeforeAll
    fun setup() {
        assumeTrue(isPostgresReachable(), "foundation Postgres not reachable at $jdbcUrl")
        db = Db.open(cfg.database)
        assertTrue(db.migrate().success)
        bindServices()
    }

    @AfterAll
    fun teardown() {
        if (::db.isInitialized) db.close()
    }

    private fun bindServices() {
        val projectRepo = JdbcProjectRepository(db.dataSource)
        val envRepo = JdbcEnvironmentRepository(db.dataSource)
        val applicationRepo = JdbcApplicationRepository(db.dataSource)
        val serviceRepo = JdbcServiceRepository(db.dataSource)
        val deploymentRepo = JdbcDeploymentRepository(db.dataSource)
        val auditRepo = JdbcAuditRepository(db.dataSource)
        val relationships = RelationshipValidator(projectRepo, applicationRepo)
        services = ControlServices(
            projects = ProjectService(projectRepo, auditRepo, actor = "dev"),
            environments = EnvironmentService(projectRepo, envRepo, auditRepo, actor = "dev"),
            applications = ApplicationService(applicationRepo, relationships, auditRepo, actor = "dev"),
            services = ServiceService(serviceRepo, relationships, auditRepo, actor = "dev"),
            deployments = DeploymentService(
                deploymentRepo,
                serviceRepo,
                applicationRepo,
                envRepo,
                auditRepo,
                actor = "dev",
            ),
            projectTrees = ProjectTreeService(
                projectRepo,
                envRepo,
                applicationRepo,
                serviceRepo,
                deploymentRepo,
            ),
            idempotency = JdbcIdempotencyStore(db.dataSource),
            resources = CompatibilityResourceRepository(
                jdbc = JdbcResourceRepository(db.dataSource),
                projects = projectRepo,
                environments = envRepo,
                applications = applicationRepo,
                services = serviceRepo,
                deployments = deploymentRepo,
                audit = auditRepo,
                actor = "dev",
            ),
            resourceEvents = JdbcResourceEventRepository(db.dataSource),
            kindRegistry = buildKindRegistry(),
        )
    }

    private fun countEventsForResource(resourceId: String): Int =
        db.dataSource.connection.use { conn ->
            conn.prepareStatement(
                "SELECT COUNT(*) FROM resource_events WHERE resource_id = ?",
            ).use { ps ->
                ps.setString(1, resourceId)
                ps.executeQuery().use { rs ->
                    check(rs.next())
                    rs.getInt(1)
                }
            }
        }

    private fun eventTypesForResource(resourceId: String): List<String> =
        db.dataSource.connection.use { conn ->
            conn.prepareStatement(
                """
                SELECT event_type FROM resource_events
                WHERE resource_id = ?
                ORDER BY resource_version ASC
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, resourceId)
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) add(rs.getString(1))
                    }
                }
            }
        }

    private fun isPostgresReachable(): Boolean =
        try {
            DriverManager.getConnection(jdbcUrl, dbUser, dbPassword).use { true }
        } catch (_: Exception) {
            false
        }

    private fun ApplicationTestBuilder.jsonClient() = createClient {
        install(ContentNegotiation) {
            json(json)
        }
    }

    private fun withApp(block: suspend ApplicationTestBuilder.() -> Unit) {
        testApplication {
            application {
                forgeControlModule(
                    cfg = cfg,
                    readiness = Readiness(initial = true),
                    services = services,
                )
            }
            block()
        }
    }

    private fun basePath() = "/v1/projects/$project/environments/$environment/widgets"

    @Test
    @Order(1)
    fun postCreatesWidgetWithIdAndResourceVersion() = withApp {
        val client = jsonClient()
        widgetName = "sample-${UUID.randomUUID().toString().take(8)}"
        val response = client.post(basePath()) {
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put("apiVersion", JsonPrimitive("forge.dev/v1"))
                    put("kind", JsonPrimitive("Widget"))
                    put(
                        "metadata",
                        buildJsonObject {
                            put("name", JsonPrimitive(widgetName))
                            put("labels", buildJsonObject {})
                        },
                    )
                    put("spec", buildJsonObject { put("size", JsonPrimitive("large")) })
                },
            )
        }
        assertEquals(HttpStatusCode.Created, response.status)
        val body = response.body<ResourceEnvelopeResponse>()
        assertTrue(body.metadata.id.startsWith("wgt_"))
        assertTrue(body.metadata.resourceVersion.toLong() >= 1L)
        assertEquals("large", body.spec["size"]!!.jsonPrimitive.content)
        assertEquals(widgetName, body.metadata.name)
    }

    @Test
    @Order(2)
    fun putSucceedsWithCurrentVersionAndConflictsWhenStale() = withApp {
        val client = jsonClient()
        val current = client.get("${basePath()}/$widgetName").body<ResourceEnvelopeResponse>()
        val rv = current.metadata.resourceVersion.toLong()

        val ok = client.put("${basePath()}/$widgetName") {
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put(
                        "metadata",
                        buildJsonObject { put("resourceVersion", JsonPrimitive(rv.toString())) },
                    )
                    put("spec", buildJsonObject { put("size", JsonPrimitive("medium")) })
                },
            )
        }
        assertEquals(HttpStatusCode.OK, ok.status)
        val updated = ok.body<ResourceEnvelopeResponse>()
        assertTrue(updated.metadata.resourceVersion.toLong() > rv)
        assertEquals("medium", updated.spec["size"]!!.jsonPrimitive.content)

        val conflict = client.put("${basePath()}/$widgetName") {
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put(
                        "metadata",
                        buildJsonObject { put("resourceVersion", JsonPrimitive(rv.toString())) },
                    )
                    put("spec", buildJsonObject { put("size", JsonPrimitive("stale")) })
                },
            )
        }
        assertEquals(HttpStatusCode.Conflict, conflict.status)
        val err = conflict.body<ErrorEnvelope>()
        assertEquals("resource_version_conflict", err.error.code)
        assertEquals(
            updated.metadata.resourceVersion,
            err.error.details?.get("currentResourceVersion"),
        )
    }

    @Test
    @Order(3)
    fun patchMergeAndJsonPatchMutateSpec() = withApp {
        val client = jsonClient()
        val before = client.get("${basePath()}/$widgetName").body<ResourceEnvelopeResponse>()

        val merge = client.patch("${basePath()}/$widgetName") {
            contentType(ContentType.parse("application/merge-patch+json"))
            setBody(buildJsonObject { put("spec", buildJsonObject { put("size", JsonPrimitive("tiny")) }) })
        }
        assertEquals(HttpStatusCode.OK, merge.status)
        assertEquals("tiny", merge.body<ResourceEnvelopeResponse>().spec["size"]!!.jsonPrimitive.content)

        val jsonPatch = client.patch("${basePath()}/$widgetName") {
            contentType(ContentType.parse("application/json-patch+json"))
            setBody(
                buildJsonArray {
                    add(
                        buildJsonObject {
                            put("op", JsonPrimitive("replace"))
                            put("path", JsonPrimitive("/spec/size"))
                            put("value", JsonPrimitive("patched"))
                        },
                    )
                },
            )
        }
        assertEquals(HttpStatusCode.OK, jsonPatch.status)
        val after = jsonPatch.body<ResourceEnvelopeResponse>()
        assertEquals("patched", after.spec["size"]!!.jsonPrimitive.content)
        assertTrue(after.metadata.resourceVersion.toLong() > before.metadata.resourceVersion.toLong())
    }

    @Test
    @Order(4)
    fun idempotentPostReplaysAndConflictsOnBodyMismatch() = withApp {
        val client = jsonClient()
        val key = "idem-${UUID.randomUUID()}"
        val name = "idem-${UUID.randomUUID().toString().take(8)}"
        val body = buildJsonObject {
            put("apiVersion", JsonPrimitive("forge.dev/v1"))
            put("kind", JsonPrimitive("Widget"))
            put("metadata", buildJsonObject { put("name", JsonPrimitive(name)) })
            put("spec", buildJsonObject { put("size", JsonPrimitive("a")) })
        }
        val first = client.post(basePath()) {
            contentType(ContentType.Application.Json)
            header("Idempotency-Key", key)
            setBody(body)
        }
        assertEquals(HttpStatusCode.Created, first.status)
        val firstBody = first.body<ResourceEnvelopeResponse>()

        val replay = client.post(basePath()) {
            contentType(ContentType.Application.Json)
            header("Idempotency-Key", key)
            setBody(body)
        }
        assertEquals(HttpStatusCode.Created, replay.status)
        assertEquals(firstBody.metadata.id, replay.body<ResourceEnvelopeResponse>().metadata.id)

        val conflict = client.post(basePath()) {
            contentType(ContentType.Application.Json)
            header("Idempotency-Key", key)
            setBody(
                buildJsonObject {
                    put("apiVersion", JsonPrimitive("forge.dev/v1"))
                    put("kind", JsonPrimitive("Widget"))
                    put("metadata", buildJsonObject { put("name", JsonPrimitive(name)) })
                    put("spec", buildJsonObject { put("size", JsonPrimitive("b")) })
                },
            )
        }
        assertEquals(HttpStatusCode.Conflict, conflict.status)
        assertEquals("idempotency_key_conflict", conflict.body<ErrorEnvelope>().error.code)
    }

    @Test
    @Order(5)
    fun deleteSoftRemovesFromSubsequentGets() = withApp {
        val client = jsonClient()
        val del = client.delete("${basePath()}/$widgetName")
        assertEquals(HttpStatusCode.NoContent, del.status)
        val get = client.get("${basePath()}/$widgetName")
        assertEquals(HttpStatusCode.NotFound, get.status)
    }

    @Test
    @Order(6)
    fun unknownPluralReturnsKindNotRegistered() = withApp {
        val client = jsonClient()
        val response = client.get("/v1/projects/$project/environments/$environment/unknownkinds/x")
        assertEquals(HttpStatusCode.NotFound, response.status)
        assertEquals("kind_not_registered", response.body<ErrorEnvelope>().error.code)
    }

    @Test
    @Order(7)
    fun putChangingSpecBumpsGenerationIdenticalPutDoesNot() = withApp {
        val client = jsonClient()
        val name = "gen-${UUID.randomUUID().toString().take(8)}"
        val created = client.post(basePath()) {
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put("apiVersion", JsonPrimitive("forge.dev/v1"))
                    put("kind", JsonPrimitive("Widget"))
                    put("metadata", buildJsonObject { put("name", JsonPrimitive(name)) })
                    put("spec", buildJsonObject { put("size", JsonPrimitive("a")) })
                },
            )
        }.body<ResourceEnvelopeResponse>()
        assertEquals(1L, created.metadata.generation)

        val first = client.put("${basePath()}/$name") {
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put(
                        "metadata",
                        buildJsonObject {
                            put("resourceVersion", JsonPrimitive(created.metadata.resourceVersion))
                        },
                    )
                    put("spec", buildJsonObject { put("size", JsonPrimitive("b")) })
                },
            )
        }.body<ResourceEnvelopeResponse>()
        assertEquals(2L, first.metadata.generation)

        val second = client.put("${basePath()}/$name") {
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put(
                        "metadata",
                        buildJsonObject {
                            put("resourceVersion", JsonPrimitive(first.metadata.resourceVersion))
                        },
                    )
                    put("spec", buildJsonObject { put("size", JsonPrimitive("b")) })
                },
            )
        }.body<ResourceEnvelopeResponse>()
        assertEquals(2L, second.metadata.generation)
        assertTrue(second.metadata.resourceVersion.toLong() > first.metadata.resourceVersion.toLong())

        val labelOnly = client.put("${basePath()}/$name") {
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put(
                        "metadata",
                        buildJsonObject {
                            put("resourceVersion", JsonPrimitive(second.metadata.resourceVersion))
                            put("labels", buildJsonObject { put("tier", JsonPrimitive("backend")) })
                        },
                    )
                    put("spec", buildJsonObject { put("size", JsonPrimitive("b")) })
                },
            )
        }.body<ResourceEnvelopeResponse>()
        assertEquals(2L, labelOnly.metadata.generation)
        assertEquals("backend", labelOnly.metadata.labels["tier"]!!.jsonPrimitive.content)
    }

    @Test
    @Order(8)
    fun statusAndSpecEndpointsRejectCrossWrites() = withApp {
        val client = jsonClient()
        val name = "cross-${UUID.randomUUID().toString().take(8)}"
        val created = client.post(basePath()) {
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put("metadata", buildJsonObject { put("name", JsonPrimitive(name)) })
                    put("spec", buildJsonObject { put("size", JsonPrimitive("x")) })
                },
            )
        }.body<ResourceEnvelopeResponse>()

        val statusWithSpec = client.put("${basePath()}/$name/status") {
            contentType(ContentType.Application.Json)
            header("X-Forge-Controller", "widget-controller")
            setBody(
                buildJsonObject {
                    put(
                        "metadata",
                        buildJsonObject {
                            put("resourceVersion", JsonPrimitive(created.metadata.resourceVersion))
                        },
                    )
                    put("spec", buildJsonObject { put("size", JsonPrimitive("nope")) })
                    put("status", buildJsonObject { put("phase", JsonPrimitive("Ready")) })
                },
            )
        }
        assertEquals(HttpStatusCode.BadRequest, statusWithSpec.status)
        assertEquals(
            "status_subresource_spec_forbidden",
            statusWithSpec.body<ErrorEnvelope>().error.code,
        )

        val mainWithStatus = client.put("${basePath()}/$name") {
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put(
                        "metadata",
                        buildJsonObject {
                            put("resourceVersion", JsonPrimitive(created.metadata.resourceVersion))
                        },
                    )
                    put("spec", buildJsonObject { put("size", JsonPrimitive("x")) })
                    put("status", buildJsonObject { put("phase", JsonPrimitive("Ready")) })
                },
            )
        }
        assertEquals(HttpStatusCode.BadRequest, mainWithStatus.status)
        assertEquals(
            "spec_endpoint_status_forbidden",
            mainWithStatus.body<ErrorEnvelope>().error.code,
        )
    }

    @Test
    @Order(9)
    fun statusWriteRequiresMatchingControllerAndPreservesGeneration() = withApp {
        val client = jsonClient()
        val name = "st-${UUID.randomUUID().toString().take(8)}"
        val created = client.post(basePath()) {
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put("metadata", buildJsonObject { put("name", JsonPrimitive(name)) })
                    put("spec", buildJsonObject { put("size", JsonPrimitive("s")) })
                },
            )
        }.body<ResourceEnvelopeResponse>()
        assertEquals(1L, created.metadata.generation)

        val forbidden = client.put("${basePath()}/$name/status") {
            contentType(ContentType.Application.Json)
            header("X-Forge-Controller", "wrong-controller")
            setBody(
                buildJsonObject {
                    put(
                        "metadata",
                        buildJsonObject {
                            put("resourceVersion", JsonPrimitive(created.metadata.resourceVersion))
                        },
                    )
                    put(
                        "status",
                        buildJsonObject {
                            put("phase", JsonPrimitive("Ready"))
                            put("observedGeneration", JsonPrimitive(1))
                        },
                    )
                },
            )
        }
        assertEquals(HttpStatusCode.Forbidden, forbidden.status)
        assertEquals("status_writer_not_recognized", forbidden.body<ErrorEnvelope>().error.code)

        val ok = client.put("${basePath()}/$name/status") {
            contentType(ContentType.Application.Json)
            header("X-Forge-Controller", "widget-controller")
            setBody(
                buildJsonObject {
                    put(
                        "metadata",
                        buildJsonObject {
                            put("resourceVersion", JsonPrimitive(created.metadata.resourceVersion))
                        },
                    )
                    put(
                        "status",
                        buildJsonObject {
                            put("phase", JsonPrimitive("Ready"))
                            put("observedGeneration", JsonPrimitive(1))
                            put(
                                "conditions",
                                buildJsonArray {
                                    add(
                                        buildJsonObject {
                                            put("type", JsonPrimitive("Available"))
                                            put("status", JsonPrimitive("True"))
                                            put("reason", JsonPrimitive("OK"))
                                            put("message", JsonPrimitive("ready"))
                                        },
                                    )
                                },
                            )
                        },
                    )
                },
            )
        }
        assertEquals(HttpStatusCode.OK, ok.status)
        val statusBody = ok.body<ResourceEnvelopeResponse>()
        assertEquals(1L, statusBody.metadata.generation)
        assertEquals("Ready", statusBody.status["phase"]!!.jsonPrimitive.content)
        assertEquals("1", statusBody.status["observedGeneration"]!!.jsonPrimitive.content)
        assertTrue(statusBody.metadata.resourceVersion.toLong() > created.metadata.resourceVersion.toLong())

        val get = client.get("${basePath()}/$name").body<ResourceEnvelopeResponse>()
        assertEquals(1L, get.metadata.generation)
        assertEquals("1", get.status["observedGeneration"]!!.jsonPrimitive.content)
        assertEquals("Ready", get.status["phase"]!!.jsonPrimitive.content)
    }

    @Test
    @Order(10)
    fun labelSelectorPhaseFilterAndPagination() = withApp {
        val client = jsonClient()
        val prefix = "lst-${UUID.randomUUID().toString().take(8)}"
        val specs = listOf(
            Triple("$prefix-a", "web", "Ready"),
            Triple("$prefix-b", "web", "Pending"),
            Triple("$prefix-c", "cache", "Ready"),
            Triple("$prefix-d", "web", "Ready"),
            Triple("$prefix-e", "api", "Ready"),
        )
        for ((name, tier, phase) in specs) {
            val created = client.post(basePath()) {
                contentType(ContentType.Application.Json)
                setBody(
                    buildJsonObject {
                        put("apiVersion", JsonPrimitive("forge.dev/v1"))
                        put("kind", JsonPrimitive("Widget"))
                        put(
                            "metadata",
                            buildJsonObject {
                                put("name", JsonPrimitive(name))
                                put("labels", buildJsonObject { put("tier", JsonPrimitive(tier)) })
                            },
                        )
                        put("spec", buildJsonObject { put("size", JsonPrimitive("s")) })
                    },
                )
            }.body<ResourceEnvelopeResponse>()
            if (phase == "Ready") {
                client.put("${basePath()}/$name/status") {
                    contentType(ContentType.Application.Json)
                    header("X-Forge-Controller", "widget-controller")
                    setBody(
                        buildJsonObject {
                            put(
                                "metadata",
                                buildJsonObject {
                                    put("resourceVersion", JsonPrimitive(created.metadata.resourceVersion))
                                },
                            )
                            put("status", buildJsonObject { put("phase", JsonPrimitive("Ready")) })
                        },
                    )
                }
            }
        }

        val byLabel = client.get("${basePath()}?labelSelector=tier=web&namePrefix=$prefix")
            .body<ListEnvelope>()
        assertEquals("WidgetList", byLabel.kind)
        assertEquals(3, byLabel.items.size)
        assertTrue(byLabel.items.all { it.metadata.labels["tier"]!!.jsonPrimitive.content == "web" })

        val byPhase = client.get("${basePath()}?labelSelector=tier=web&phase=Ready&namePrefix=$prefix")
            .body<ListEnvelope>()
        assertEquals(2, byPhase.items.size)
        assertTrue(byPhase.items.all { it.status["phase"]!!.jsonPrimitive.content == "Ready" })

        val page1 = client.get("${basePath()}?namePrefix=$prefix&limit=2").body<ListEnvelope>()
        assertEquals(2, page1.items.size)
        assertTrue(!page1.nextCursor.isNullOrBlank())
        val page2 = client.get("${basePath()}?namePrefix=$prefix&limit=2&cursor=${page1.nextCursor}")
            .body<ListEnvelope>()
        assertEquals(2, page2.items.size)
        assertTrue(!page2.nextCursor.isNullOrBlank())
        val page3 = client.get("${basePath()}?namePrefix=$prefix&limit=2&cursor=${page2.nextCursor}")
            .body<ListEnvelope>()
        assertEquals(1, page3.items.size)
        assertNull(page3.nextCursor)

        val allNames = (page1.items + page2.items + page3.items).map { it.metadata.name }
        assertEquals(5, allNames.size)
        assertEquals(allNames.sorted(), allNames)
        assertEquals(allNames.toSet().size, allNames.size)

        val full = client.get("${basePath()}?namePrefix=$prefix&limit=50").body<ListEnvelope>()
        val maxItemRv = full.items.maxOf { it.metadata.resourceVersion.toLong() }
        assertEquals(maxItemRv.toString(), full.resourceVersion)
    }

    @Test
    @Order(11)
    fun malformedLabelSelectorAndCursorReturn400() = withApp {
        val client = jsonClient()
        val badSel = client.get("${basePath()}?labelSelector=tier===")
        assertEquals(HttpStatusCode.BadRequest, badSel.status)
        assertEquals("invalid_label_selector", badSel.body<ErrorEnvelope>().error.code)

        val badCursor = client.get("${basePath()}?cursor=not-a-cursor")
        assertEquals(HttpStatusCode.BadRequest, badCursor.status)
        assertEquals("invalid_cursor", badCursor.body<ErrorEnvelope>().error.code)
    }

    @Test
    @Order(12)
    fun mutationsEmitExactlyOneDurableEventEach() = withApp {
        val client = jsonClient()
        val name = "evt-${UUID.randomUUID().toString().take(8)}"
        val created = client.post(basePath()) {
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put("apiVersion", JsonPrimitive("forge.dev/v1"))
                    put("kind", JsonPrimitive("Widget"))
                    put("metadata", buildJsonObject { put("name", JsonPrimitive(name)) })
                    put("spec", buildJsonObject { put("size", JsonPrimitive("s")) })
                },
            )
        }.body<ResourceEnvelopeResponse>()
        assertEquals(1, countEventsForResource(created.metadata.id))

        val patched = client.patch("${basePath()}/$name") {
            contentType(ContentType.parse("application/merge-patch+json"))
            setBody(
                buildJsonObject {
                    put(
                        "metadata",
                        buildJsonObject {
                            put("resourceVersion", JsonPrimitive(created.metadata.resourceVersion))
                        },
                    )
                    put("spec", buildJsonObject { put("size", JsonPrimitive("m")) })
                },
            )
        }.body<ResourceEnvelopeResponse>()
        assertEquals(2, countEventsForResource(created.metadata.id))

        client.put("${basePath()}/$name/status") {
            header("X-Forge-Controller", "widget-controller")
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put(
                        "metadata",
                        buildJsonObject {
                            put("resourceVersion", JsonPrimitive(patched.metadata.resourceVersion))
                        },
                    )
                    put("status", buildJsonObject { put("phase", JsonPrimitive("Ready")) })
                },
            )
        }
        assertEquals(3, countEventsForResource(created.metadata.id))

        client.delete("${basePath()}/$name")
        assertEquals(4, countEventsForResource(created.metadata.id))
        assertEquals(
            listOf("ADDED", "MODIFIED", "STATUS_MODIFIED", "DELETED"),
            eventTypesForResource(created.metadata.id),
        )
    }

    @Test
    @Order(13)
    fun listResourceVersionCanStartWatchWithoutMissedUpdates() = withApp {
        val client = jsonClient()
        val name = "watch-${UUID.randomUUID().toString().take(8)}"
        client.post(basePath()) {
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put("apiVersion", JsonPrimitive("forge.dev/v1"))
                    put("kind", JsonPrimitive("Widget"))
                    put("metadata", buildJsonObject { put("name", JsonPrimitive(name)) })
                    put("spec", buildJsonObject { put("size", JsonPrimitive("s")) })
                },
            )
        }
        val listRv = client.get(basePath()).body<ListEnvelope>().resourceVersion.toLong()

        // Mutation after the list snapshot — must be visible when watching from that RV.
        val liveName = "live-${UUID.randomUUID().toString().take(8)}"
        val live = client.post(basePath()) {
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put("apiVersion", JsonPrimitive("forge.dev/v1"))
                    put("kind", JsonPrimitive("Widget"))
                    put("metadata", buildJsonObject { put("name", JsonPrimitive(liveName)) })
                    put("spec", buildJsonObject { put("size", JsonPrimitive("l")) })
                },
            )
        }.body<ResourceEnvelopeResponse>()
        assertTrue(live.metadata.resourceVersion.toLong() > listRv)

        // Replay buffer (same query the SSE watch handler uses) must include the post-list mutation.
        // (Ktor testApplication cannot complete an open SSE stream, so we assert the watch data path.)
        val replay = JdbcResourceEventRepository(db.dataSource).listAfter(
            kind = "Widget",
            organization = "default",
            since = listRv,
        )
        assertTrue(
            replay.any {
                it.eventType == ResourceEventType.ADDED && it.resourceName == liveName
            },
            "expected ADDED for $liveName after list resourceVersion=$listRv; got ${replay.map { it.resourceName to it.eventType }}",
        )
    }

    @Test
    @Order(14)
    fun staleWatchCursorReturns410Gone() = withApp {
        val client = jsonClient()
        val first = client.post(basePath()) {
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put("apiVersion", JsonPrimitive("forge.dev/v1"))
                    put("kind", JsonPrimitive("Widget"))
                    put(
                        "metadata",
                        buildJsonObject {
                            put("name", JsonPrimitive("stale-${UUID.randomUUID().toString().take(8)}"))
                        },
                    )
                    put("spec", buildJsonObject { put("size", JsonPrimitive("s")) })
                },
            )
        }.body<ResourceEnvelopeResponse>()
        val firstRv = first.metadata.resourceVersion.toLong()

        // Compact away everything at or before this mutation so the retention floor advances.
        db.dataSource.connection.use { conn ->
            conn.prepareStatement(
                "DELETE FROM resource_events WHERE resource_version <= ?",
            ).use { ps ->
                ps.setLong(1, firstRv)
                assertTrue(ps.executeUpdate() >= 1)
            }
        }

        client.post(basePath()) {
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put("apiVersion", JsonPrimitive("forge.dev/v1"))
                    put("kind", JsonPrimitive("Widget"))
                    put(
                        "metadata",
                        buildJsonObject {
                            put("name", JsonPrimitive("newer-${UUID.randomUUID().toString().take(8)}"))
                        },
                    )
                    put("spec", buildJsonObject { put("size", JsonPrimitive("m")) })
                },
            )
        }

        val gone = client.get("/v1/watch/widgets?since=0") {
            accept(ContentType.Text.EventStream)
        }
        assertEquals(HttpStatusCode.Gone, gone.status)
        assertEquals("resource_version_too_old", gone.body<ErrorEnvelope>().error.code)
    }

    @Test
    @Order(15)
    fun deleteWithFinalizersLeavesTerminatingUntilLastFinalizerRemoved() = withApp {
        val client = jsonClient()
        val name = "fin-${UUID.randomUUID().toString().take(8)}"
        val created = client.post(basePath()) {
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put("apiVersion", JsonPrimitive("forge.dev/v1"))
                    put("kind", JsonPrimitive("Widget"))
                    put(
                        "metadata",
                        buildJsonObject {
                            put("name", JsonPrimitive(name))
                            put(
                                "finalizers",
                                buildJsonArray {
                                    add(JsonPrimitive("widget.forge.dev/finalizer"))
                                },
                            )
                        },
                    )
                    put("spec", buildJsonObject { put("size", JsonPrimitive("s")) })
                },
            )
        }.body<ResourceEnvelopeResponse>()

        val del = client.delete("${basePath()}/$name")
        assertEquals(HttpStatusCode.OK, del.status)
        val terminating = del.body<ResourceEnvelopeResponse>()
        assertEquals("Terminating", terminating.status["phase"]!!.jsonPrimitive.content)
        assertTrue(terminating.metadata.deletionTimestamp != null)
        assertEquals(listOf("widget.forge.dev/finalizer"), terminating.metadata.finalizers.map { it.jsonPrimitive.content })

        val stillVisible = client.get("${basePath()}/$name")
        assertEquals(HttpStatusCode.OK, stillVisible.status)
        assertTrue(
            eventTypesForResource(created.metadata.id).contains("MODIFIED"),
            "terminating delete must emit MODIFIED, not DELETED yet",
        )
        assertTrue(!eventTypesForResource(created.metadata.id).contains("DELETED"))

        val forbidden = client.patch("${basePath()}/$name/finalizers") {
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put(
                        "remove",
                        buildJsonArray { add(JsonPrimitive("widget.forge.dev/finalizer")) },
                    )
                },
            )
        }
        assertEquals(HttpStatusCode.Forbidden, forbidden.status)
        assertEquals("forbidden_finalizer", forbidden.body<ErrorEnvelope>().error.code)

        val cleared = client.patch("${basePath()}/$name/finalizers") {
            header("X-Forge-Controller", "widget-controller")
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put(
                        "remove",
                        buildJsonArray { add(JsonPrimitive("widget.forge.dev/finalizer")) },
                    )
                },
            )
        }
        assertEquals(HttpStatusCode.NoContent, cleared.status)
        assertEquals(HttpStatusCode.NotFound, client.get("${basePath()}/$name").status)
        assertTrue(eventTypesForResource(created.metadata.id).contains("DELETED"))
    }

    @Test
    @Order(16)
    fun protectedKindRequiresDeleteConfirmation() = withApp {
        val client = jsonClient()
        val name = "vault-${UUID.randomUUID().toString().take(8)}"
        val path = "/v1/projects/$project/environments/$environment/vaults"
        client.post(path) {
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put("apiVersion", JsonPrimitive("forge.dev/v1"))
                    put("kind", JsonPrimitive("Vault"))
                    put("metadata", buildJsonObject { put("name", JsonPrimitive(name)) })
                    put("spec", buildJsonObject { put("engine", JsonPrimitive("kv")) })
                },
            )
        }
        val denied = client.delete("$path/$name")
        assertEquals(HttpStatusCode.Conflict, denied.status)
        assertEquals("delete_confirmation_required", denied.body<ErrorEnvelope>().error.code)

        val ok = client.delete("$path/$name") {
            header("X-Forge-Delete-Confirmation", name)
        }
        assertEquals(HttpStatusCode.NoContent, ok.status)
        assertEquals(HttpStatusCode.NotFound, client.get("$path/$name").status)
    }

    @Test
    @Order(17)
    fun ownerReferenceCycleIsRejected() = withApp {
        val client = jsonClient()
        val parentName = "own-p-${UUID.randomUUID().toString().take(8)}"
        val childName = "own-c-${UUID.randomUUID().toString().take(8)}"
        val parent = client.post(basePath()) {
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put("apiVersion", JsonPrimitive("forge.dev/v1"))
                    put("kind", JsonPrimitive("Widget"))
                    put("metadata", buildJsonObject { put("name", JsonPrimitive(parentName)) })
                    put("spec", buildJsonObject { put("size", JsonPrimitive("p")) })
                },
            )
        }.body<ResourceEnvelopeResponse>()
        val child = client.post(basePath()) {
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put("apiVersion", JsonPrimitive("forge.dev/v1"))
                    put("kind", JsonPrimitive("Widget"))
                    put(
                        "metadata",
                        buildJsonObject {
                            put("name", JsonPrimitive(childName))
                            put(
                                "ownerRefs",
                                buildJsonArray {
                                    add(
                                        buildJsonObject {
                                            put("kind", JsonPrimitive("Widget"))
                                            put("id", JsonPrimitive(parent.metadata.id))
                                        },
                                    )
                                },
                            )
                        },
                    )
                    put("spec", buildJsonObject { put("size", JsonPrimitive("c")) })
                },
            )
        }.body<ResourceEnvelopeResponse>()

        val cycle = client.put("${basePath()}/$parentName") {
            contentType(ContentType.Application.Json)
            setBody(
                buildJsonObject {
                    put(
                        "metadata",
                        buildJsonObject {
                            put("resourceVersion", JsonPrimitive(parent.metadata.resourceVersion))
                            put(
                                "ownerRefs",
                                buildJsonArray {
                                    add(
                                        buildJsonObject {
                                            put("kind", JsonPrimitive("Widget"))
                                            put("id", JsonPrimitive(child.metadata.id))
                                        },
                                    )
                                },
                            )
                        },
                    )
                    put("spec", buildJsonObject { put("size", JsonPrimitive("p")) })
                },
            )
        }
        assertEquals(HttpStatusCode.BadRequest, cycle.status)
        assertEquals("owner_reference_cycle", cycle.body<ErrorEnvelope>().error.code)
    }
}

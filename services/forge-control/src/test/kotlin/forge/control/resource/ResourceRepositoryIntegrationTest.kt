package forge.control.resource

import forge.control.config.DatabaseConfig
import forge.control.db.Db
import forge.control.repo.RepositoryException
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
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
import kotlin.test.assertFailsWith
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

/**
 * Resource repository + V20_01 migration tests against foundation Postgres
 * (`jdbc:postgresql://127.0.0.1:5001/forge`). Skipped when the DB is unreachable.
 *
 * Uses a private fixture kind (`Widget`) registered only in test source.
 */
@TestInstance(TestInstance.Lifecycle.PER_CLASS)
@TestMethodOrder(MethodOrderer.OrderAnnotation::class)
class ResourceRepositoryIntegrationTest {
    private val jdbcUrl = System.getenv("DATABASE_URL")
        ?: "jdbc:postgresql://127.0.0.1:5001/forge"
    private val dbUser = System.getenv("DATABASE_USER") ?: "forge"
    private val dbPassword = System.getenv("DATABASE_PASSWORD") ?: "forge"

    private lateinit var db: Db
    private lateinit var resources: JdbcResourceRepository
    private lateinit var kindRegistry: KindRegistry

    private val widgetKind = KindDescriptor(
        kind = "Widget",
        plural = "widgets",
        scope = ResourceScope.Environment,
        schemaVersion = 1,
        owningController = "widget-controller",
        idPrefix = "wgt",
    )

    @BeforeAll
    fun setup() {
        assumeTrue(isPostgresReachable(), "foundation Postgres not reachable at $jdbcUrl")
        db = Db.open(
            DatabaseConfig(
                url = jdbcUrl,
                user = dbUser,
                password = dbPassword,
                schema = "control",
                poolMax = 4,
                migrateOnStart = true,
            ),
        )
        val result = db.migrate()
        assertTrue(result.success)
        resources = JdbcResourceRepository(db.dataSource)
        kindRegistry = KindRegistry().also { it.register(widgetKind) }
    }

    @AfterAll
    fun teardown() {
        if (::db.isInitialized) db.close()
    }

    private fun isPostgresReachable(): Boolean =
        try {
            DriverManager.getConnection(jdbcUrl, dbUser, dbPassword).use { true }
        } catch (_: Exception) {
            false
        }

    @Test
    @Order(1)
    fun migrationCreatesResourcesTableAndSequence() {
        db.dataSource.connection.use { conn ->
            conn.prepareStatement(
                """
                SELECT column_name FROM information_schema.columns
                WHERE table_schema = 'control' AND table_name = 'resources'
                ORDER BY ordinal_position
                """.trimIndent(),
            ).use { ps ->
                ps.executeQuery().use { rs ->
                    val columns = buildList {
                        while (rs.next()) add(rs.getString(1))
                    }
                    assertEquals(
                        listOf(
                            "id",
                            "kind",
                            "api_version",
                            "organization",
                            "project",
                            "environment",
                            "name",
                            "generation",
                            "resource_version",
                            "labels",
                            "annotations",
                            "spec",
                            "status",
                            "owner_refs",
                            "finalizers",
                            "created_at",
                            "updated_at",
                            "deleted_at",
                        ),
                        columns,
                    )
                }
            }
            conn.prepareStatement(
                """
                SELECT 1 FROM pg_sequences
                WHERE schemaname = 'control' AND sequencename = 'resource_version_seq'
                """.trimIndent(),
            ).use { ps ->
                ps.executeQuery().use { rs ->
                    assertTrue(rs.next(), "resource_version_seq missing")
                }
            }
            conn.prepareStatement(
                """
                SELECT indexname FROM pg_indexes
                WHERE schemaname = 'control' AND tablename = 'resources'
                ORDER BY indexname
                """.trimIndent(),
            ).use { ps ->
                ps.executeQuery().use { rs ->
                    val indexes = buildList {
                        while (rs.next()) add(rs.getString(1))
                    }
                    assertTrue(indexes.contains("resources_scope_unique_env"))
                    assertTrue(indexes.contains("resources_scope_unique_project"))
                    assertTrue(indexes.contains("resources_scope_unique_cluster"))
                    assertTrue(indexes.contains("resources_kind_scope_idx"))
                }
            }
        }
        assertNotNull(kindRegistry.get("Widget"))
        assertNotNull(kindRegistry.byPlural("widgets"))
    }

    @Test
    @Order(2)
    fun insertAndFindByIdRoundTripsEnvelopeFields() {
        val id = Ulid.next("wgt")
        val labels = buildJsonObject {
            put("tier", JsonPrimitive("gold"))
            put("team", JsonPrimitive("platform"))
        }
        val spec = buildJsonObject {
            put("size", JsonPrimitive(3))
            put(
                "nested",
                buildJsonObject {
                    put("enabled", JsonPrimitive(true))
                },
            )
        }
        val inserted = resources.insert(
            NewResourceRow(
                id = id,
                kind = "Widget",
                organization = "default",
                project = "invoice-platform",
                environment = "production",
                name = "widget-${UUID.randomUUID().toString().take(8)}",
                labels = labels,
                spec = spec,
            ),
        )

        assertEquals(1L, inserted.generation)
        assertTrue(inserted.resourceVersion >= 1L)
        assertEquals(labels, inserted.labels)
        assertEquals(spec, inserted.spec)
        assertEquals(JsonObject(emptyMap()), inserted.status)

        val loaded = resources.findById(id)
        assertNotNull(loaded)
        assertEquals(inserted.id, loaded.id)
        assertEquals(inserted.name, loaded.name)
        assertEquals(labels, loaded.labels)
        assertEquals(spec, loaded.spec)
        assertEquals(inserted.resourceVersion, loaded.resourceVersion)

        val envelope = loaded.toEnvelope()
        assertEquals("forge.dev/v1", envelope.apiVersion)
        assertEquals("Widget", envelope.kind)
        assertEquals(labels, envelope.metadata.labels)
        assertEquals(spec, envelope.spec)

        val byName = resources.findByScopeAndName(
            kind = "Widget",
            organization = "default",
            project = "invoice-platform",
            environment = "production",
            name = inserted.name,
        )
        assertNotNull(byName)
        assertEquals(id, byName.id)
    }

    @Test
    @Order(3)
    fun duplicateEnvironmentScopedNameThrowsConflict() {
        val name = "dup-env-${UUID.randomUUID().toString().take(8)}"
        val base = NewResourceRow(
            id = Ulid.next("wgt"),
            kind = "Widget",
            organization = "default",
            project = "invoice-platform",
            environment = "staging",
            name = name,
            spec = buildJsonObject { put("n", JsonPrimitive(1)) },
        )
        resources.insert(base)
        assertFailsWith<RepositoryException.Conflict> {
            resources.insert(base.copy(id = Ulid.next("wgt")))
        }
    }

    @Test
    @Order(4)
    fun clusterScopedDuplicateNameConflictsDespiteNullProject() {
        // Plain UNIQUE (kind, org, project, name) would NOT conflict when project IS NULL
        // because Postgres treats NULL as distinct. The partial unique index must fire.
        val name = "cluster-${UUID.randomUUID().toString().take(8)}"
        val first = NewResourceRow(
            id = Ulid.next("wgt"),
            kind = "Widget",
            organization = "default",
            project = null,
            environment = null,
            name = name,
            spec = buildJsonObject { put("n", JsonPrimitive(1)) },
        )
        resources.insert(first)
        assertFailsWith<RepositoryException.Conflict> {
            resources.insert(first.copy(id = Ulid.next("wgt")))
        }

        // Same name under two different non-null projects must NOT conflict with each other
        // (project scope uses a different partial index).
        val projectName = "proj-uniq-${UUID.randomUUID().toString().take(8)}"
        resources.insert(
            NewResourceRow(
                id = Ulid.next("wgt"),
                kind = "Widget",
                organization = "default",
                project = "project-a",
                environment = null,
                name = projectName,
                spec = buildJsonObject { put("n", JsonPrimitive(1)) },
            ),
        )
        resources.insert(
            NewResourceRow(
                id = Ulid.next("wgt"),
                kind = "Widget",
                organization = "default",
                project = "project-b",
                environment = null,
                name = projectName,
                spec = buildJsonObject { put("n", JsonPrimitive(2)) },
            ),
        )

        // Duplicate within the same project (null environment) conflicts.
        assertFailsWith<RepositoryException.Conflict> {
            resources.insert(
                NewResourceRow(
                    id = Ulid.next("wgt"),
                    kind = "Widget",
                    organization = "default",
                    project = "project-a",
                    environment = null,
                    name = projectName,
                    spec = buildJsonObject { put("n", JsonPrimitive(3)) },
                ),
            )
        }
    }

    @Test
    @Order(5)
    fun environmentWithoutProjectViolatesCheckConstraint() {
        assertFailsWith<RepositoryException.ConstraintViolation> {
            resources.insert(
                NewResourceRow(
                    id = Ulid.next("wgt"),
                    kind = "Widget",
                    organization = "default",
                    project = null,
                    environment = "production",
                    name = "bad-scope-${UUID.randomUUID().toString().take(8)}",
                    spec = buildJsonObject { put("n", JsonPrimitive(1)) },
                ),
            )
        }
    }

    @Test
    @Order(6)
    fun findByIdIgnoresSoftDeletedRows() {
        val id = Ulid.next("wgt")
        resources.insert(
            NewResourceRow(
                id = id,
                kind = "Widget",
                organization = "default",
                project = "invoice-platform",
                environment = "dev",
                name = "soft-del-${UUID.randomUUID().toString().take(8)}",
                spec = buildJsonObject { put("n", JsonPrimitive(1)) },
            ),
        )
        db.dataSource.connection.use { conn ->
            conn.prepareStatement(
                "UPDATE resources SET deleted_at = now() WHERE id = ?",
            ).use { ps ->
                ps.setString(1, id)
                assertEquals(1, ps.executeUpdate())
            }
        }
        assertNull(resources.findById(id))
    }
}

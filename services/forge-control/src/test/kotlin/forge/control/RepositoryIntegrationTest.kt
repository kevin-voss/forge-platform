package forge.control

import forge.control.config.DatabaseConfig
import forge.control.db.Db
import forge.control.repo.JdbcApplicationRepository
import forge.control.repo.JdbcAuditRepository
import forge.control.repo.JdbcDeploymentRepository
import forge.control.repo.JdbcEnvironmentRepository
import forge.control.repo.JdbcProjectRepository
import forge.control.repo.JdbcServiceRepository
import forge.control.repo.RepositoryException
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
 * Repository + migration integration tests against foundation Postgres
 * (`jdbc:postgresql://127.0.0.1:5001/forge`). Skipped when the DB is unreachable.
 */
@TestInstance(TestInstance.Lifecycle.PER_CLASS)
@TestMethodOrder(MethodOrderer.OrderAnnotation::class)
class RepositoryIntegrationTest {
    private val jdbcUrl = System.getenv("DATABASE_URL")
        ?: "jdbc:postgresql://127.0.0.1:5001/forge"
    private val dbUser = System.getenv("DATABASE_USER") ?: "forge"
    private val dbPassword = System.getenv("DATABASE_PASSWORD") ?: "forge"

    private lateinit var db: Db
    private lateinit var projects: JdbcProjectRepository
    private lateinit var environments: JdbcEnvironmentRepository
    private lateinit var applications: JdbcApplicationRepository
    private lateinit var services: JdbcServiceRepository
    private lateinit var deployments: JdbcDeploymentRepository
    private lateinit var audit: JdbcAuditRepository

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
        bindRepos()
    }

    @AfterAll
    fun teardown() {
        if (::db.isInitialized) db.close()
    }

    private fun bindRepos() {
        projects = JdbcProjectRepository(db.dataSource)
        environments = JdbcEnvironmentRepository(db.dataSource)
        applications = JdbcApplicationRepository(db.dataSource)
        services = JdbcServiceRepository(db.dataSource)
        deployments = JdbcDeploymentRepository(db.dataSource)
        audit = JdbcAuditRepository(db.dataSource)
    }

    private fun isPostgresReachable(): Boolean =
        try {
            DriverManager.getConnection(jdbcUrl, dbUser, dbPassword).use { true }
        } catch (_: Exception) {
            false
        }

    @Test
    @Order(1)
    fun migrationsCreateExpectedTables() {
        db.dataSource.connection.use { conn ->
            conn.prepareStatement(
                """
                SELECT table_name FROM information_schema.tables
                WHERE table_schema = 'control'
                ORDER BY table_name
                """.trimIndent(),
            ).use { ps ->
                ps.executeQuery().use { rs ->
                    val tables = buildList {
                        while (rs.next()) add(rs.getString(1))
                    }
                    assertTrue(
                        tables.containsAll(
                            listOf(
                                "applications",
                                "audit_log",
                                "deployment_events",
                                "deployments",
                                "environments",
                                "flyway_schema_history",
                                "placements",
                                "projects",
                                "reconcile_status",
                                "services",
                            ),
                        ),
                    )
                }
            }
        }
    }

    @Test
    @Order(2)
    fun projectCrudRoundTrip() {
        val created = projects.create("Demo", "demo-${UUID.randomUUID()}")
        assertNotNull(created.id)
        assertNotNull(created.createdAt)
        assertNotNull(created.updatedAt)

        val found = projects.findById(created.id)
        assertNotNull(found)
        assertEquals(created.id, found.id)
        assertEquals(created.name, found.name)
        assertEquals(created.slug, found.slug)

        val updated = projects.update(created.id, name = "Demo Updated", slug = null)
        assertEquals("Demo Updated", updated.name)
        assertEquals(created.slug, updated.slug)

        projects.delete(updated.id)
        assertNull(projects.findById(updated.id))
    }

    @Test
    @Order(3)
    fun hierarchyCrudAndFkRestrict() {
        val project = projects.create("Hier", "hier-${UUID.randomUUID()}")
        val env = environments.create(project.id, "development")
        val app = applications.create(project.id, "backend")
        val svc = services.create(app.id, "api", 8080)
        val dep = deployments.create(svc.id, env.id, "registry.local/api:1", desiredReplicas = 2)

        assertEquals(1, environments.list(project.id).size)
        assertEquals(env.id, environments.list(project.id).single().id)
        assertEquals(app.id, applications.list(project.id).single().id)
        assertEquals(svc.id, services.list(app.id).single().id)
        assertEquals(dep.id, deployments.listByService(svc.id).single().id)
        assertEquals(2, deployments.findById(dep.id)?.desiredReplicas)
        assertEquals("pending", deployments.findById(dep.id)?.status)

        val entry = audit.append(
            entityType = "deployment",
            entityId = dep.id,
            action = "create",
            actor = "test",
            detailJson = """{"image":"registry.local/api:1"}""",
        )
        assertEquals(listOf(entry.id), audit.listByEntity("deployment", dep.id).map { it.id })

        assertFailsWith<RepositoryException.ConstraintViolation> {
            projects.delete(project.id)
        }

        assertFailsWith<RepositoryException.ConstraintViolation> {
            applications.delete(app.id)
        }

        deployments.delete(dep.id)
        services.delete(svc.id)
        applications.delete(app.id)
        environments.delete(env.id)
        projects.delete(project.id)
        assertNull(projects.findById(project.id))
    }

    @Test
    @Order(4)
    fun uniqueSlugConflict() {
        val slug = "unique-${UUID.randomUUID()}"
        projects.create("A", slug)
        assertFailsWith<RepositoryException.Conflict> {
            projects.create("B", slug)
        }
    }

    @Test
    @Order(5)
    fun checkConstraintRejectsBadPort() {
        val project = projects.create("Port", "port-${UUID.randomUUID()}")
        val app = applications.create(project.id, "app")
        val ex = assertFailsWith<java.sql.SQLException> {
            db.dataSource.connection.use { conn ->
                conn.prepareStatement(
                    """
                    INSERT INTO services (id, application_id, name, port, created_at, updated_at)
                    VALUES (?, ?, 'bad', 0, NOW(), NOW())
                    """.trimIndent(),
                ).use { ps ->
                    ps.setObject(1, UUID.randomUUID())
                    ps.setObject(2, app.id)
                    ps.executeUpdate()
                }
            }
        }
        assertEquals("23514", ex.sqlState)
    }

    @Test
    @Order(6)
    fun rowsSurviveReconnect() {
        val project = projects.create("Persist", "persist-${UUID.randomUUID()}")
        db.close()
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
        bindRepos()

        val found = projects.findById(project.id)
        assertEquals(project.id, found?.id)
        assertEquals(project.slug, found?.slug)
    }
}

package forge.control.resource

import forge.control.repo.instant
import forge.control.repo.runSql
import forge.control.repo.withConnection
import java.sql.ResultSet
import java.sql.Timestamp
import java.sql.Types
import java.time.Instant
import javax.sql.DataSource

/**
 * Storage seam for declarative resources.
 *
 * [JdbcResourceRepository] backs new kinds with [control.resources]. Step 20.07
 * may add a second implementation that projects grandfathered Application /
 * Service / Deployment rows through the same interface without changing callers.
 */
interface ResourceRepository {
    fun insert(row: NewResourceRow): ResourceRow

    fun findById(id: String): ResourceRow?

    fun findByScopeAndName(
        kind: String,
        organization: String,
        project: String?,
        environment: String?,
        name: String,
    ): ResourceRow?
}

class JdbcResourceRepository(
    private val dataSource: DataSource,
) : ResourceRepository {
    override fun insert(row: NewResourceRow): ResourceRow = runSql {
        val now = Instant.now()
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                INSERT INTO resources (
                    id, kind, api_version, organization, project, environment, name,
                    generation, resource_version, labels, annotations, spec, status,
                    owner_refs, finalizers, created_at, updated_at
                ) VALUES (
                    ?, ?, ?, ?, ?, ?, ?,
                    1, nextval('resource_version_seq'), ?::jsonb, ?::jsonb, ?::jsonb, '{}'::jsonb,
                    ?::jsonb, ?::jsonb, ?, ?
                )
                RETURNING
                    id, kind, api_version, organization, project, environment, name,
                    generation, resource_version,
                    labels::text AS labels,
                    annotations::text AS annotations,
                    spec::text AS spec,
                    status::text AS status,
                    owner_refs::text AS owner_refs,
                    finalizers::text AS finalizers,
                    created_at, updated_at, deleted_at
                """.trimIndent(),
            ).use { ps ->
                var i = 1
                ps.setString(i++, row.id)
                ps.setString(i++, row.kind)
                ps.setString(i++, row.apiVersion)
                ps.setString(i++, row.organization)
                setNullableString(ps, i++, row.project)
                setNullableString(ps, i++, row.environment)
                ps.setString(i++, row.name)
                ps.setString(i++, row.labels.encode())
                ps.setString(i++, row.annotations.encode())
                ps.setString(i++, row.spec.encode())
                ps.setString(i++, row.ownerRefs.encode())
                ps.setString(i++, row.finalizers.encode())
                ps.setTimestamp(i++, Timestamp.from(now))
                ps.setTimestamp(i, Timestamp.from(now))
                ps.executeQuery().use { rs ->
                    check(rs.next()) { "INSERT RETURNING produced no row" }
                    mapRow(rs)
                }
            }
        }
    }

    override fun findById(id: String): ResourceRow? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT
                    id, kind, api_version, organization, project, environment, name,
                    generation, resource_version,
                    labels::text AS labels,
                    annotations::text AS annotations,
                    spec::text AS spec,
                    status::text AS status,
                    owner_refs::text AS owner_refs,
                    finalizers::text AS finalizers,
                    created_at, updated_at, deleted_at
                FROM resources
                WHERE id = ? AND deleted_at IS NULL
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, id)
                ps.executeQuery().use { rs ->
                    if (rs.next()) mapRow(rs) else null
                }
            }
        }
    }

    override fun findByScopeAndName(
        kind: String,
        organization: String,
        project: String?,
        environment: String?,
        name: String,
    ): ResourceRow? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT
                    id, kind, api_version, organization, project, environment, name,
                    generation, resource_version,
                    labels::text AS labels,
                    annotations::text AS annotations,
                    spec::text AS spec,
                    status::text AS status,
                    owner_refs::text AS owner_refs,
                    finalizers::text AS finalizers,
                    created_at, updated_at, deleted_at
                FROM resources
                WHERE kind = ?
                  AND organization = ?
                  AND name = ?
                  AND deleted_at IS NULL
                  AND (
                      (?::text IS NULL AND project IS NULL)
                      OR project = ?
                  )
                  AND (
                      (?::text IS NULL AND environment IS NULL)
                      OR environment = ?
                  )
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, kind)
                ps.setString(2, organization)
                ps.setString(3, name)
                setNullableString(ps, 4, project)
                setNullableString(ps, 5, project)
                setNullableString(ps, 6, environment)
                setNullableString(ps, 7, environment)
                ps.executeQuery().use { rs ->
                    if (rs.next()) mapRow(rs) else null
                }
            }
        }
    }

    private fun mapRow(rs: ResultSet): ResourceRow =
        ResourceRow(
            id = rs.getString("id"),
            kind = rs.getString("kind"),
            apiVersion = rs.getString("api_version"),
            organization = rs.getString("organization"),
            project = rs.getString("project"),
            environment = rs.getString("environment"),
            name = rs.getString("name"),
            generation = rs.getLong("generation"),
            resourceVersion = rs.getLong("resource_version"),
            labels = parseJsonObject(rs.getString("labels")),
            annotations = parseJsonObject(rs.getString("annotations")),
            spec = parseJsonObject(rs.getString("spec")),
            status = parseJsonObject(rs.getString("status")),
            ownerRefs = parseJsonArray(rs.getString("owner_refs")),
            finalizers = parseJsonArray(rs.getString("finalizers")),
            createdAt = rs.instant("created_at"),
            updatedAt = rs.instant("updated_at"),
            deletedAt = rs.getTimestamp("deleted_at")?.toInstant(),
            deletionTimestamp = null,
        )

    private fun setNullableString(ps: java.sql.PreparedStatement, index: Int, value: String?) {
        if (value == null) {
            ps.setNull(index, Types.VARCHAR)
        } else {
            ps.setString(index, value)
        }
    }
}

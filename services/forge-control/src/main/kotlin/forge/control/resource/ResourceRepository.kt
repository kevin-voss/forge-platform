package forge.control.resource

import forge.control.http.ApiException
import forge.control.repo.instant
import forge.control.repo.runSql
import forge.control.repo.withConnection
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
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

    /** Full replace of writable fields; fails with version conflict when [expectedVersion] mismatches. */
    fun replace(
        id: String,
        expectedVersion: Long,
        labels: JsonObject,
        annotations: JsonObject,
        spec: JsonObject,
        ownerRefs: JsonArray,
        finalizers: JsonArray,
        bumpGeneration: Boolean = false,
    ): ResourceRow

    /** Spec/labels/annotations update with the same version guard as [replace]. */
    fun patch(
        id: String,
        expectedVersion: Long,
        labels: JsonObject,
        annotations: JsonObject,
        spec: JsonObject,
        bumpGeneration: Boolean = false,
    ): ResourceRow

    /**
     * Status-only update (no generation bump). Same [expectedVersion] optimistic concurrency
     * as [replace]. Used by the `/status` subresource.
     */
    fun updateStatus(
        id: String,
        expectedVersion: Long,
        status: JsonObject,
    ): ResourceRow

    /** Soft-delete: sets [deleted_at] immediately (finalizer-aware delete arrives in 20.06). */
    fun softDelete(id: String): ResourceRow
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

    override fun replace(
        id: String,
        expectedVersion: Long,
        labels: JsonObject,
        annotations: JsonObject,
        spec: JsonObject,
        ownerRefs: JsonArray,
        finalizers: JsonArray,
        bumpGeneration: Boolean,
    ): ResourceRow = versionedUpdate(
        id = id,
        expectedVersion = expectedVersion,
        labels = labels,
        annotations = annotations,
        spec = spec,
        ownerRefs = ownerRefs,
        finalizers = finalizers,
        updateOwnerRefs = true,
        updateFinalizers = true,
        bumpGeneration = bumpGeneration,
    )

    override fun patch(
        id: String,
        expectedVersion: Long,
        labels: JsonObject,
        annotations: JsonObject,
        spec: JsonObject,
        bumpGeneration: Boolean,
    ): ResourceRow = versionedUpdate(
        id = id,
        expectedVersion = expectedVersion,
        labels = labels,
        annotations = annotations,
        spec = spec,
        ownerRefs = null,
        finalizers = null,
        updateOwnerRefs = false,
        updateFinalizers = false,
        bumpGeneration = bumpGeneration,
    )

    override fun updateStatus(
        id: String,
        expectedVersion: Long,
        status: JsonObject,
    ): ResourceRow = runSql {
        val now = Instant.now()
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                UPDATE resources
                SET status = ?::jsonb,
                    updated_at = ?,
                    resource_version = nextval('resource_version_seq')
                WHERE id = ? AND resource_version = ? AND deleted_at IS NULL
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
                ps.setString(1, status.encode())
                ps.setTimestamp(2, Timestamp.from(now))
                ps.setString(3, id)
                ps.setLong(4, expectedVersion)
                ps.executeQuery().use { rs ->
                    if (rs.next()) {
                        return@withConnection mapRow(rs)
                    }
                }
            }
            val current = findByIdOn(conn, id)
                ?: throw ApiException.NotFound(
                    "resource not found",
                    details = mapOf("id" to id),
                    code = "not_found",
                )
            throw ResourceVersionGuard.conflict(expectedVersion, current.resourceVersion)
        }
    }

    override fun softDelete(id: String): ResourceRow = runSql {
        val now = Instant.now()
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                UPDATE resources
                SET deleted_at = ?,
                    updated_at = ?,
                    resource_version = nextval('resource_version_seq')
                WHERE id = ? AND deleted_at IS NULL
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
                ps.setTimestamp(1, Timestamp.from(now))
                ps.setTimestamp(2, Timestamp.from(now))
                ps.setString(3, id)
                ps.executeQuery().use { rs ->
                    if (!rs.next()) {
                        throw ApiException.NotFound(
                            "resource not found",
                            details = mapOf("id" to id),
                            code = "not_found",
                        )
                    }
                    mapRow(rs)
                }
            }
        }
    }

    private fun versionedUpdate(
        id: String,
        expectedVersion: Long,
        labels: JsonObject,
        annotations: JsonObject,
        spec: JsonObject,
        ownerRefs: JsonArray?,
        finalizers: JsonArray?,
        updateOwnerRefs: Boolean,
        updateFinalizers: Boolean,
        bumpGeneration: Boolean,
    ): ResourceRow = runSql {
        val now = Instant.now()
        dataSource.withConnection { conn ->
            val sql = buildString {
                append(
                    """
                    UPDATE resources
                    SET labels = ?::jsonb,
                        annotations = ?::jsonb,
                        spec = ?::jsonb,
                        updated_at = ?,
                        resource_version = nextval('resource_version_seq')
                    """.trimIndent(),
                )
                if (bumpGeneration) append(",\n    generation = generation + 1")
                if (updateOwnerRefs) append(",\n    owner_refs = ?::jsonb")
                if (updateFinalizers) append(",\n    finalizers = ?::jsonb")
                append(
                    """
                    
                    WHERE id = ? AND resource_version = ? AND deleted_at IS NULL
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
                )
            }
            conn.prepareStatement(sql).use { ps ->
                var i = 1
                ps.setString(i++, labels.encode())
                ps.setString(i++, annotations.encode())
                ps.setString(i++, spec.encode())
                ps.setTimestamp(i++, Timestamp.from(now))
                if (updateOwnerRefs) {
                    ps.setString(i++, (ownerRefs ?: JsonArray(emptyList())).encode())
                }
                if (updateFinalizers) {
                    ps.setString(i++, (finalizers ?: JsonArray(emptyList())).encode())
                }
                ps.setString(i++, id)
                ps.setLong(i, expectedVersion)
                ps.executeQuery().use { rs ->
                    if (rs.next()) {
                        return@withConnection mapRow(rs)
                    }
                }
            }
            // Version mismatch or missing — distinguish for the caller.
            val current = findByIdOn(conn, id)
                ?: throw ApiException.NotFound(
                    "resource not found",
                    details = mapOf("id" to id),
                    code = "not_found",
                )
            throw ResourceVersionGuard.conflict(expectedVersion, current.resourceVersion)
        }
    }

    private fun findByIdOn(conn: java.sql.Connection, id: String): ResourceRow? =
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

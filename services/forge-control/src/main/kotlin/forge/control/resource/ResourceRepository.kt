package forge.control.resource

import forge.control.http.ApiException
import forge.control.http.RequestId
import forge.control.repo.instant
import forge.control.repo.runSql
import forge.control.repo.withConnection
import forge.control.telemetry.Telemetry
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import java.sql.Connection
import java.sql.ResultSet
import java.sql.Timestamp
import java.sql.Types
import java.time.Instant
import javax.sql.DataSource

private val eventPayloadJson = Json {
    ignoreUnknownKeys = true
    encodeDefaults = true
    explicitNulls = false
}

/** Shared SELECT/RETURNING projection for [control.resources]. */
private val RESOURCE_SELECT = """
    id, kind, api_version, organization, project, environment, name,
    generation, resource_version,
    labels::text AS labels,
    annotations::text AS annotations,
    spec::text AS spec,
    status::text AS status,
    owner_refs::text AS owner_refs,
    finalizers::text AS finalizers,
    created_at, updated_at, deleted_at, deletion_timestamp
""".trimIndent()

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

    /**
     * Terminal soft-delete: sets [deletion_timestamp] (if unset) and [deleted_at],
     * emits [DELETED]. Used when there are no remaining finalizers.
     */
    fun softDelete(id: String): ResourceRow

    /**
     * Marks the resource Terminating: sets [deletion_timestamp], `status.phase=Terminating`,
     * emits [MODIFIED]. Idempotent when already terminating. Caller must ensure finalizers
     * are non-empty (otherwise use [softDelete]).
     */
    fun markTerminating(id: String): ResourceRow

    /**
     * Replaces the finalizer list. When [deletion_timestamp] is set and the resulting
     * list is empty, performs the terminal [softDelete] path (emits [DELETED]).
     */
    fun replaceFinalizers(id: String, finalizers: JsonArray): ResourceRow

    /** Resources that list [ownerId] in `owner_refs` (not yet terminal-deleted). */
    fun findOwnedBy(ownerId: String): List<ResourceRow>

    /** Clears owner refs pointing at [ownerId] (cascade=orphan). */
    fun clearOwnerRefsTo(ownerId: String): Int

    /**
     * Filtered, cursor-paginated list. [ResourceListResult.resourceVersion] is
     * `max(resource_version)` over the full matched set (ignoring pagination).
     */
    fun list(query: ResourceListQuery): ResourceListResult
}

data class ResourceListQuery(
    val kind: String,
    val organization: String,
    val project: String?,
    val environment: String?,
    val selector: LabelSelector = LabelSelector(emptyList()),
    val phase: String? = null,
    val namePrefix: String? = null,
    val limit: Int,
    val cursor: CursorCodec.Cursor? = null,
)

data class ResourceListResult(
    val items: List<ResourceRow>,
    val resourceVersion: Long,
    val nextCursor: String?,
)

class JdbcResourceRepository(
    private val dataSource: DataSource,
    private val events: ResourceEventRepository = JdbcResourceEventRepository(dataSource),
) : ResourceRepository {
    override fun insert(row: NewResourceRow): ResourceRow = runSql {
        val now = Instant.now()
        dataSource.withTransaction { conn ->
            val inserted = conn.prepareStatement(
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
                RETURNING $RESOURCE_SELECT
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
            emitEvent(conn, inserted, ResourceEventType.ADDED)
            inserted
        }
    }

    override fun findById(id: String): ResourceRow? = runSql {
        dataSource.withConnection { conn ->
            findByIdOn(conn, id)
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
                SELECT $RESOURCE_SELECT
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
        dataSource.withTransaction { conn ->
            val updated = conn.prepareStatement(
                """
                UPDATE resources
                SET status = ?::jsonb,
                    updated_at = ?,
                    resource_version = nextval('resource_version_seq')
                WHERE id = ? AND resource_version = ? AND deleted_at IS NULL
                RETURNING $RESOURCE_SELECT
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, status.encode())
                ps.setTimestamp(2, Timestamp.from(now))
                ps.setString(3, id)
                ps.setLong(4, expectedVersion)
                ps.executeQuery().use { rs ->
                    if (rs.next()) mapRow(rs) else null
                }
            }
            if (updated != null) {
                emitEvent(conn, updated, ResourceEventType.STATUS_MODIFIED)
                return@withTransaction updated
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
        dataSource.withTransaction { conn ->
            terminalDeleteOn(conn, id)
        }
    }

    override fun markTerminating(id: String): ResourceRow = runSql {
        val now = Instant.now()
        dataSource.withTransaction { conn ->
            val current = findByIdOn(conn, id)
                ?: throw ApiException.NotFound(
                    "resource not found",
                    details = mapOf("id" to id),
                    code = "not_found",
                )
            if (current.deletionTimestamp != null) {
                return@withTransaction current
            }
            val status = JsonObject(
                current.status + ("phase" to JsonPrimitive(PhaseDerivation.Phase.Terminating.name)),
            )
            val updated = conn.prepareStatement(
                """
                UPDATE resources
                SET deletion_timestamp = ?,
                    status = ?::jsonb,
                    updated_at = ?,
                    resource_version = nextval('resource_version_seq')
                WHERE id = ? AND deleted_at IS NULL AND deletion_timestamp IS NULL
                RETURNING $RESOURCE_SELECT
                """.trimIndent(),
            ).use { ps ->
                ps.setTimestamp(1, Timestamp.from(now))
                ps.setString(2, status.encode())
                ps.setTimestamp(3, Timestamp.from(now))
                ps.setString(4, id)
                ps.executeQuery().use { rs ->
                    if (rs.next()) mapRow(rs) else null
                }
            } ?: findByIdOn(conn, id)
                ?: throw ApiException.NotFound(
                    "resource not found",
                    details = mapOf("id" to id),
                    code = "not_found",
                )
            emitEvent(conn, updated, ResourceEventType.MODIFIED)
            Telemetry.current().recordResourceTerminating(updated.kind)
            updated
        }
    }

    override fun replaceFinalizers(id: String, finalizers: JsonArray): ResourceRow = runSql {
        val now = Instant.now()
        dataSource.withTransaction { conn ->
            val current = findByIdOn(conn, id)
                ?: throw ApiException.NotFound(
                    "resource not found",
                    details = mapOf("id" to id),
                    code = "not_found",
                )
            if (current.deletionTimestamp != null && Finalizers.isEmpty(finalizers)) {
                return@withTransaction terminalDeleteOn(conn, id)
            }
            val updated = conn.prepareStatement(
                """
                UPDATE resources
                SET finalizers = ?::jsonb,
                    updated_at = ?,
                    resource_version = nextval('resource_version_seq')
                WHERE id = ? AND deleted_at IS NULL
                RETURNING $RESOURCE_SELECT
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, finalizers.encode())
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
            emitEvent(conn, updated, ResourceEventType.MODIFIED)
            updated
        }
    }

    override fun findOwnedBy(ownerId: String): List<ResourceRow> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT $RESOURCE_SELECT
                FROM resources
                WHERE deleted_at IS NULL
                  AND EXISTS (
                      SELECT 1
                      FROM jsonb_array_elements(owner_refs) AS ref
                      WHERE ref->>'id' = ?
                  )
                ORDER BY name ASC, id ASC
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, ownerId)
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) add(mapRow(rs))
                    }
                }
            }
        }
    }

    override fun clearOwnerRefsTo(ownerId: String): Int = runSql {
        val now = Instant.now()
        dataSource.withTransaction { conn ->
            val children = findOwnedByOn(conn, ownerId)
            var cleared = 0
            for (child in children) {
                val remaining = OwnerRefs.parse(child.ownerRefs).filter { it.id != ownerId }
                val encoded = OwnerRefs.encode(remaining)
                conn.prepareStatement(
                    """
                    UPDATE resources
                    SET owner_refs = ?::jsonb,
                        updated_at = ?,
                        resource_version = nextval('resource_version_seq')
                    WHERE id = ? AND deleted_at IS NULL
                    RETURNING $RESOURCE_SELECT
                    """.trimIndent(),
                ).use { ps ->
                    ps.setString(1, encoded.encode())
                    ps.setTimestamp(2, Timestamp.from(now))
                    ps.setString(3, child.id)
                    ps.executeQuery().use { rs ->
                        if (rs.next()) {
                            val updated = mapRow(rs)
                            emitEvent(conn, updated, ResourceEventType.MODIFIED)
                            cleared++
                        }
                    }
                }
            }
            cleared
        }
    }

    override fun list(query: ResourceListQuery): ResourceListResult = runSql {
        dataSource.withConnection { conn ->
            val filter = buildListFilter(query)
            val resourceVersion = conn.prepareStatement(
                """
                SELECT COALESCE(MAX(resource_version), 0)
                FROM resources
                WHERE ${filter.whereSql}
                """.trimIndent(),
            ).use { ps ->
                bindFilter(ps, filter.params, start = 1)
                ps.executeQuery().use { rs ->
                    check(rs.next())
                    rs.getLong(1)
                }
            }

            val pageLimit = query.limit.coerceAtLeast(1)
            val rows = conn.prepareStatement(
                """
                SELECT $RESOURCE_SELECT
                FROM resources
                WHERE ${filter.whereSql}
                ORDER BY name ASC, id ASC
                LIMIT ?
                """.trimIndent(),
            ).use { ps ->
                var i = bindFilter(ps, filter.params, start = 1)
                ps.setInt(i, pageLimit + 1)
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) add(mapRow(rs))
                    }
                }
            }

            val hasMore = rows.size > pageLimit
            val page = if (hasMore) rows.take(pageLimit) else rows
            val nextCursor = if (hasMore && page.isNotEmpty()) {
                val last = page.last()
                CursorCodec.encode(last.name, last.id)
            } else {
                null
            }
            ResourceListResult(
                items = page,
                resourceVersion = resourceVersion,
                nextCursor = nextCursor,
            )
        }
    }

    private data class ListFilter(
        val whereSql: String,
        val params: List<Any?>,
    )

    private fun buildListFilter(query: ResourceListQuery): ListFilter {
        val clauses = mutableListOf(
            "kind = ?",
            "organization = ?",
            "deleted_at IS NULL",
            """
            (
                (?::text IS NULL AND project IS NULL)
                OR project = ?
            )
            """.trimIndent(),
            """
            (
                (?::text IS NULL AND environment IS NULL)
                OR environment = ?
            )
            """.trimIndent(),
        )
        val params = mutableListOf<Any?>(
            query.kind,
            query.organization,
            query.project,
            query.project,
            query.environment,
            query.environment,
        )

        val selectorFrag = LabelSelectorSql.render(query.selector)
        if (selectorFrag.sql != "TRUE") {
            clauses += selectorFrag.sql
            params.addAll(selectorFrag.params)
        }

        if (!query.phase.isNullOrBlank()) {
            clauses += "status->>'phase' = ?"
            params.add(query.phase)
        }
        if (!query.namePrefix.isNullOrEmpty()) {
            clauses += "name LIKE ? ESCAPE '\\'"
            params.add(escapeLikePrefix(query.namePrefix) + "%")
        }
        if (query.cursor != null) {
            clauses += "(name, id) > (?, ?)"
            params.add(query.cursor.name)
            params.add(query.cursor.id)
        }

        return ListFilter(whereSql = clauses.joinToString(" AND "), params = params)
    }

    private fun bindFilter(
        ps: java.sql.PreparedStatement,
        params: List<Any?>,
        start: Int,
    ): Int {
        var i = start
        for (param in params) {
            when (param) {
                null -> ps.setNull(i, Types.VARCHAR)
                is String -> ps.setString(i, param)
                is Int -> ps.setInt(i, param)
                is Long -> ps.setLong(i, param)
                else -> ps.setObject(i, param)
            }
            i++
        }
        return i
    }

    private fun escapeLikePrefix(prefix: String): String =
        prefix
            .replace("\\", "\\\\")
            .replace("%", "\\%")
            .replace("_", "\\_")

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
        dataSource.withTransaction { conn ->
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
                    RETURNING $RESOURCE_SELECT
                    """.trimIndent(),
                )
            }
            val updated = conn.prepareStatement(sql).use { ps ->
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
                    if (rs.next()) mapRow(rs) else null
                }
            }
            if (updated != null) {
                // Clearing the last finalizer while terminating completes deletion.
                if (
                    updated.deletionTimestamp != null &&
                    Finalizers.isEmpty(updated.finalizers)
                ) {
                    return@withTransaction terminalDeleteOn(conn, id)
                }
                emitEvent(conn, updated, ResourceEventType.MODIFIED)
                return@withTransaction updated
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

    private fun terminalDeleteOn(conn: Connection, id: String): ResourceRow {
        val now = Instant.now()
        val deleted = conn.prepareStatement(
            """
            UPDATE resources
            SET deletion_timestamp = COALESCE(deletion_timestamp, ?),
                deleted_at = ?,
                status = jsonb_set(
                    COALESCE(status, '{}'::jsonb),
                    '{phase}',
                    '"Terminating"'::jsonb,
                    true
                ),
                updated_at = ?,
                resource_version = nextval('resource_version_seq')
            WHERE id = ? AND deleted_at IS NULL
            RETURNING $RESOURCE_SELECT
            """.trimIndent(),
        ).use { ps ->
            ps.setTimestamp(1, Timestamp.from(now))
            ps.setTimestamp(2, Timestamp.from(now))
            ps.setTimestamp(3, Timestamp.from(now))
            ps.setString(4, id)
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
        emitEvent(conn, deleted, ResourceEventType.DELETED)
        return deleted
    }

    private fun emitEvent(conn: Connection, row: ResourceRow, type: ResourceEventType) {
        val payloadEncoded = eventPayloadJson.encodeToString(
            ResourceEnvelopeResponse.serializer(),
            row.toResponse(),
        )
        events.appendOn(
            conn,
            NewResourceEvent(
                resourceVersion = row.resourceVersion,
                eventId = Ulid.next("evt"),
                eventType = type,
                kind = row.kind,
                organization = row.organization,
                project = row.project,
                environment = row.environment,
                resourceId = row.id,
                resourceName = row.name,
                generation = row.generation,
                payload = parseJsonObject(payloadEncoded),
                actor = null,
                requestId = RequestId.current(),
            ),
        )
        Telemetry.current().recordResourceEventEmitted(row.kind, type.name)
    }

    private inline fun <T> DataSource.withTransaction(block: (Connection) -> T): T =
        withConnection { conn ->
            val previous = conn.autoCommit
            conn.autoCommit = false
            try {
                val result = block(conn)
                conn.commit()
                result
            } catch (e: Exception) {
                try {
                    conn.rollback()
                } catch (_: Exception) {
                    // Preserve the original failure.
                }
                throw e
            } finally {
                try {
                    conn.autoCommit = previous
                } catch (_: Exception) {
                    // Connection may already be closed.
                }
            }
        }

    private fun findByIdOn(conn: Connection, id: String): ResourceRow? =
        conn.prepareStatement(
            """
            SELECT $RESOURCE_SELECT
            FROM resources
            WHERE id = ? AND deleted_at IS NULL
            """.trimIndent(),
        ).use { ps ->
            ps.setString(1, id)
            ps.executeQuery().use { rs ->
                if (rs.next()) mapRow(rs) else null
            }
        }

    private fun findOwnedByOn(conn: Connection, ownerId: String): List<ResourceRow> =
        conn.prepareStatement(
            """
            SELECT $RESOURCE_SELECT
            FROM resources
            WHERE deleted_at IS NULL
              AND EXISTS (
                  SELECT 1
                  FROM jsonb_array_elements(owner_refs) AS ref
                  WHERE ref->>'id' = ?
              )
            ORDER BY name ASC, id ASC
            """.trimIndent(),
        ).use { ps ->
            ps.setString(1, ownerId)
            ps.executeQuery().use { rs ->
                buildList {
                    while (rs.next()) add(mapRow(rs))
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
            deletionTimestamp = rs.getTimestamp("deletion_timestamp")?.toInstant(),
        )

    private fun setNullableString(ps: java.sql.PreparedStatement, index: Int, value: String?) {
        if (value == null) {
            ps.setNull(index, Types.VARCHAR)
        } else {
            ps.setString(index, value)
        }
    }
}

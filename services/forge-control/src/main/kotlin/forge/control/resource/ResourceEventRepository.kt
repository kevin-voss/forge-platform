package forge.control.resource

import forge.control.repo.instant
import forge.control.repo.runSql
import forge.control.repo.withConnection
import java.sql.Connection
import java.sql.ResultSet
import java.sql.Types
import java.time.Duration
import java.time.Instant
import javax.sql.DataSource

/**
 * Append-only log of resource mutations for watch replay and live tail.
 *
 * [appendOn] must run on the same JDBC connection (and transaction) as the
 * resource write so event failure rolls back the mutation.
 */
interface ResourceEventRepository {
    fun appendOn(conn: Connection, event: NewResourceEvent)

    /** Events with [ResourceEvent.resourceVersion] strictly greater than [since], ordered ascending. */
    fun listAfter(
        kind: String,
        organization: String,
        since: Long,
        limit: Int = 500,
    ): List<ResourceEvent>

    /**
     * Oldest retained [resource_version] after applying the retention window,
     * or null when the table is empty.
     */
    fun oldestRetainedVersion(retention: Duration): Long?

    /** Deletes events older than [retention]. Returns rows removed. */
    fun purgeExpired(retention: Duration): Int
}

class JdbcResourceEventRepository(
    private val dataSource: DataSource,
) : ResourceEventRepository {
    override fun appendOn(conn: Connection, event: NewResourceEvent) {
        conn.prepareStatement(
            """
            INSERT INTO resource_events (
                resource_version, event_id, event_type, kind, organization,
                project, environment, resource_id, resource_name, generation,
                payload, actor, request_id
            ) VALUES (
                ?, ?, ?, ?, ?,
                ?, ?, ?, ?, ?,
                ?::jsonb, ?, ?
            )
            """.trimIndent(),
        ).use { ps ->
            var i = 1
            ps.setLong(i++, event.resourceVersion)
            ps.setString(i++, event.eventId)
            ps.setString(i++, event.eventType.name)
            ps.setString(i++, event.kind)
            ps.setString(i++, event.organization)
            setNullableString(ps, i++, event.project)
            setNullableString(ps, i++, event.environment)
            ps.setString(i++, event.resourceId)
            ps.setString(i++, event.resourceName)
            ps.setLong(i++, event.generation)
            ps.setString(i++, event.payload.encode())
            setNullableString(ps, i++, event.actor)
            setNullableString(ps, i, event.requestId)
            ps.executeUpdate()
        }
    }

    override fun listAfter(
        kind: String,
        organization: String,
        since: Long,
        limit: Int,
    ): List<ResourceEvent> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT
                    resource_version, event_id, event_type, kind, organization,
                    project, environment, resource_id, resource_name, generation,
                    payload::text AS payload, actor, request_id, created_at
                FROM resource_events
                WHERE kind = ?
                  AND organization = ?
                  AND resource_version > ?
                ORDER BY resource_version ASC
                LIMIT ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, kind)
                ps.setString(2, organization)
                ps.setLong(3, since)
                ps.setInt(4, limit.coerceAtLeast(1))
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) add(mapRow(rs))
                    }
                }
            }
        }
    }

    override fun oldestRetainedVersion(retention: Duration): Long? = runSql {
        dataSource.withConnection { conn ->
            val cutoff = Instant.now().minus(retention)
            // Purge outside the window first so the floor reflects retained history.
            conn.prepareStatement(
                "DELETE FROM resource_events WHERE created_at < ?",
            ).use { ps ->
                ps.setTimestamp(1, java.sql.Timestamp.from(cutoff))
                ps.executeUpdate()
            }
            conn.prepareStatement(
                "SELECT MIN(resource_version) FROM resource_events",
            ).use { ps ->
                ps.executeQuery().use { rs ->
                    check(rs.next())
                    val v = rs.getLong(1)
                    if (rs.wasNull()) null else v
                }
            }
        }
    }

    override fun purgeExpired(retention: Duration): Int = runSql {
        dataSource.withConnection { conn ->
            val cutoff = Instant.now().minus(retention)
            conn.prepareStatement(
                "DELETE FROM resource_events WHERE created_at < ?",
            ).use { ps ->
                ps.setTimestamp(1, java.sql.Timestamp.from(cutoff))
                ps.executeUpdate()
            }
        }
    }

    private fun mapRow(rs: ResultSet): ResourceEvent =
        ResourceEvent(
            resourceVersion = rs.getLong("resource_version"),
            eventId = rs.getString("event_id"),
            eventType = ResourceEventType.valueOf(rs.getString("event_type")),
            kind = rs.getString("kind"),
            organization = rs.getString("organization"),
            project = rs.getString("project"),
            environment = rs.getString("environment"),
            resourceId = rs.getString("resource_id"),
            resourceName = rs.getString("resource_name"),
            generation = rs.getLong("generation"),
            payload = parseJsonObject(rs.getString("payload")),
            actor = rs.getString("actor"),
            requestId = rs.getString("request_id"),
            createdAt = rs.instant("created_at"),
        )

    private fun setNullableString(ps: java.sql.PreparedStatement, index: Int, value: String?) {
        if (value == null) {
            ps.setNull(index, Types.VARCHAR)
        } else {
            ps.setString(index, value)
        }
    }
}

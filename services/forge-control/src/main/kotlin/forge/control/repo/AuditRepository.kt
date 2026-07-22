package forge.control.repo

import forge.control.domain.AuditEntry
import java.time.Instant
import java.util.UUID
import javax.sql.DataSource

interface AuditRepository {
    fun append(
        entityType: String,
        entityId: UUID,
        action: String,
        actor: String,
        detailJson: String = "{}",
    ): AuditEntry

    fun listByEntity(entityType: String, entityId: UUID): List<AuditEntry>
}

class JdbcAuditRepository(
    private val dataSource: DataSource,
) : AuditRepository {
    override fun append(
        entityType: String,
        entityId: UUID,
        action: String,
        actor: String,
        detailJson: String,
    ): AuditEntry = runSql {
        val id = UUID.randomUUID()
        val at = Instant.now()
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                INSERT INTO audit_log (id, entity_type, entity_id, action, actor, at, detail)
                VALUES (?, ?, ?, ?, ?, ?, ?::jsonb)
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, id)
                ps.setString(2, entityType)
                ps.setObject(3, entityId)
                ps.setString(4, action)
                ps.setString(5, actor)
                ps.setTimestamp(6, java.sql.Timestamp.from(at))
                ps.setString(7, detailJson)
                ps.executeUpdate()
            }
        }
        AuditEntry(id, entityType, entityId, action, actor, at, detailJson)
    }

    override fun listByEntity(entityType: String, entityId: UUID): List<AuditEntry> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, entity_type, entity_id, action, actor, at, detail::text AS detail
                FROM audit_log
                WHERE entity_type = ? AND entity_id = ?
                ORDER BY at
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, entityType)
                ps.setObject(2, entityId)
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) {
                            add(
                                AuditEntry(
                                    id = rs.uuid("id"),
                                    entityType = rs.getString("entity_type"),
                                    entityId = rs.uuid("entity_id"),
                                    action = rs.getString("action"),
                                    actor = rs.getString("actor"),
                                    at = rs.instant("at"),
                                    detailJson = rs.getString("detail"),
                                ),
                            )
                        }
                    }
                }
            }
        }
    }
}

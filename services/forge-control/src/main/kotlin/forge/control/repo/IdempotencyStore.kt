package forge.control.repo

import java.sql.Timestamp
import java.time.Instant
import javax.sql.DataSource

data class IdempotencyRecord(
    val key: String,
    val requestHash: String,
    val resourceType: String,
    val resourceId: String,
    val responseStatus: Int,
    val responseBody: String,
)

interface IdempotencyStore {
    fun find(key: String): IdempotencyRecord?
    fun save(record: IdempotencyRecord)
}

class JdbcIdempotencyStore(private val dataSource: DataSource) : IdempotencyStore {
    override fun find(key: String): IdempotencyRecord? = runSql {
        dataSource.withConnection { connection ->
            connection.prepareStatement(
                """SELECT key, request_hash, resource_type, resource_id, response_status, response_body
               FROM idempotency_keys WHERE key = ?""",
            ).use { statement ->
                statement.setString(1, key)
                statement.executeQuery().use { result ->
                    if (!result.next()) {
                        null
                    } else {
                        IdempotencyRecord(
                            key = result.getString("key"),
                            requestHash = result.getString("request_hash"),
                            resourceType = result.getString("resource_type"),
                            resourceId = result.getString("resource_id"),
                            responseStatus = result.getInt("response_status"),
                            responseBody = result.getString("response_body"),
                        )
                    }
                }
            }
        }
    }

    override fun save(record: IdempotencyRecord) {
        runSql {
            dataSource.withConnection { connection ->
                connection.prepareStatement(
                    """INSERT INTO idempotency_keys
               (key, request_hash, resource_type, resource_id, response_status, response_body, created_at)
               VALUES (?, ?, ?, ?, ?, ?::jsonb, ?)""",
                ).use { statement ->
                    statement.setString(1, record.key)
                    statement.setString(2, record.requestHash)
                    statement.setString(3, record.resourceType)
                    statement.setString(4, record.resourceId)
                    statement.setInt(5, record.responseStatus)
                    statement.setString(6, record.responseBody)
                    statement.setTimestamp(7, Timestamp.from(Instant.now()))
                    statement.executeUpdate()
                }
            }
            Unit
        }
    }
}

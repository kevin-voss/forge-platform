package forge.identity.auth

import forge.identity.db.runSql
import forge.identity.db.withConnection
import java.sql.Timestamp
import java.time.Instant
import javax.sql.DataSource

data class Credential(
    val userId: String,
    val hash: String,
    val updatedAt: Instant,
)

class CredentialStore(
    private val dataSource: DataSource,
) {
    fun upsert(userId: String, hash: String): Credential {
        val now = Instant.now()
        runSql {
            dataSource.withConnection { conn ->
                conn.prepareStatement(
                    """
                    INSERT INTO credentials (user_id, hash, updated_at)
                    VALUES (?, ?, ?)
                    ON CONFLICT (user_id) DO UPDATE
                      SET hash = EXCLUDED.hash, updated_at = EXCLUDED.updated_at
                    """.trimIndent(),
                ).use { ps ->
                    ps.setString(1, userId)
                    ps.setString(2, hash)
                    ps.setTimestamp(3, Timestamp.from(now))
                    ps.executeUpdate()
                }
            }
        }
        return Credential(userId, hash, now)
    }

    fun findByUserId(userId: String): Credential? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT user_id, hash, updated_at
                FROM credentials WHERE user_id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, userId)
                ps.executeQuery().use { rs ->
                    if (!rs.next()) {
                        null
                    } else {
                        Credential(
                            userId = rs.getString("user_id"),
                            hash = rs.getString("hash"),
                            updatedAt = rs.getTimestamp("updated_at").toInstant(),
                        )
                    }
                }
            }
        }
    }

    fun recordLoginAttempt(email: String, success: Boolean) {
        val normalized = email.trim()
        if (normalized.isEmpty()) return
        runSql {
            dataSource.withConnection { conn ->
                conn.prepareStatement(
                    """
                    INSERT INTO login_attempts (email, at, success)
                    VALUES (?, ?, ?)
                    """.trimIndent(),
                ).use { ps ->
                    ps.setString(1, normalized)
                    ps.setTimestamp(2, Timestamp.from(Instant.now()))
                    ps.setBoolean(3, success)
                    ps.executeUpdate()
                }
            }
        }
    }

    /** Count recent failed attempts for [email] within [windowSeconds]. */
    fun recentFailedAttempts(email: String, windowSeconds: Long): Int {
        val normalized = email.trim()
        if (normalized.isEmpty()) return 0
        val since = Instant.now().minusSeconds(windowSeconds.coerceAtLeast(1))
        return runSql {
            dataSource.withConnection { conn ->
                conn.prepareStatement(
                    """
                    SELECT COUNT(*) AS c
                    FROM login_attempts
                    WHERE email = ?
                      AND success = FALSE
                      AND at >= ?
                    """.trimIndent(),
                ).use { ps ->
                    ps.setString(1, normalized)
                    ps.setTimestamp(2, Timestamp.from(since))
                    ps.executeQuery().use { rs ->
                        if (!rs.next()) 0 else rs.getInt("c")
                    }
                }
            }
        }
    }

    fun isLockedOut(email: String, maxFails: Int, windowSeconds: Long): Boolean {
        if (maxFails < 1) return false
        return recentFailedAttempts(email, windowSeconds) >= maxFails
    }
}

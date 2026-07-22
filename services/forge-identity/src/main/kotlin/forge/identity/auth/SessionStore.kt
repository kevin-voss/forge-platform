package forge.identity.auth

import forge.identity.db.instant
import forge.identity.db.runSql
import forge.identity.db.withConnection
import forge.identity.metrics.IdentityMetrics
import java.security.MessageDigest
import java.security.SecureRandom
import java.sql.Timestamp
import java.time.Instant
import java.util.Base64
import java.util.UUID
import javax.sql.DataSource

data class Session(
    val id: String,
    val userId: String,
    val tokenHash: String,
    val createdAt: Instant,
    val expiresAt: Instant,
    val revokedAt: Instant?,
) {
    fun isActive(now: Instant = Instant.now()): Boolean =
        revokedAt == null && expiresAt.isAfter(now)
}

data class CreatedSession(
    val session: Session,
    /** Opaque bearer token; only returned once. Never persist plaintext. */
    val token: String,
)

class SessionStore(
    private val dataSource: DataSource,
    private val sessionTtlSeconds: Long = 86_400,
    private val random: SecureRandom = SecureRandom(),
) {
    fun create(userId: String, now: Instant = Instant.now()): CreatedSession {
        val id = UUID.randomUUID().toString()
        val token = generateOpaqueToken()
        val tokenHash = hashToken(token)
        val expiresAt = now.plusSeconds(sessionTtlSeconds.coerceAtLeast(1))
        runSql {
            dataSource.withConnection { conn ->
                conn.prepareStatement(
                    """
                    INSERT INTO sessions (id, user_id, token_hash, created_at, expires_at, revoked_at)
                    VALUES (?, ?, ?, ?, ?, NULL)
                    """.trimIndent(),
                ).use { ps ->
                    ps.setString(1, id)
                    ps.setString(2, userId)
                    ps.setString(3, tokenHash)
                    ps.setTimestamp(4, Timestamp.from(now))
                    ps.setTimestamp(5, Timestamp.from(expiresAt))
                    ps.executeUpdate()
                }
            }
        }
        IdentityMetrics.recordSessionCreated()
        val session = Session(
            id = id,
            userId = userId,
            tokenHash = tokenHash,
            createdAt = now,
            expiresAt = expiresAt,
            revokedAt = null,
        )
        return CreatedSession(session, token)
    }

    fun findByToken(token: String): Session? {
        if (token.isBlank()) return null
        val tokenHash = hashToken(token)
        return findByTokenHash(tokenHash)
    }

    fun findByTokenHash(tokenHash: String): Session? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, user_id, token_hash, created_at, expires_at, revoked_at
                FROM sessions WHERE token_hash = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, tokenHash)
                ps.executeQuery().use { rs ->
                    if (!rs.next()) null else rs.toSession()
                }
            }
        }
    }

    fun findById(id: String): Session? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, user_id, token_hash, created_at, expires_at, revoked_at
                FROM sessions WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, id)
                ps.executeQuery().use { rs ->
                    if (!rs.next()) null else rs.toSession()
                }
            }
        }
    }

    /**
     * Revoke session for [token]. Returns session id when a row was updated;
     * null when unknown. Idempotent for already-revoked sessions.
     */
    fun revokeByToken(token: String, now: Instant = Instant.now()): String? {
        if (token.isBlank()) return null
        val tokenHash = hashToken(token)
        return runSql {
            dataSource.withConnection { conn ->
                conn.prepareStatement(
                    """
                    UPDATE sessions
                    SET revoked_at = ?
                    WHERE token_hash = ?
                      AND revoked_at IS NULL
                    RETURNING id
                    """.trimIndent(),
                ).use { ps ->
                    ps.setTimestamp(1, Timestamp.from(now))
                    ps.setString(2, tokenHash)
                    ps.executeQuery().use { rs ->
                        if (!rs.next()) {
                            // Already revoked or unknown — fetch id if present
                            findByTokenHash(tokenHash)?.id
                        } else {
                            IdentityMetrics.recordSessionRevoked()
                            rs.getString("id")
                        }
                    }
                }
            }
        }
    }

    fun countActive(now: Instant = Instant.now()): Long = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT COUNT(*) AS c
                FROM sessions
                WHERE revoked_at IS NULL
                  AND expires_at > ?
                """.trimIndent(),
            ).use { ps ->
                ps.setTimestamp(1, Timestamp.from(now))
                ps.executeQuery().use { rs ->
                    if (!rs.next()) 0L else rs.getLong("c")
                }
            }
        }
    }

    private fun generateOpaqueToken(): String {
        val bytes = ByteArray(32)
        random.nextBytes(bytes)
        return Base64.getUrlEncoder().withoutPadding().encodeToString(bytes)
    }

    companion object {
        fun hashToken(token: String): String {
            val digest = MessageDigest.getInstance("SHA-256").digest(token.toByteArray(Charsets.UTF_8))
            return digest.joinToString("") { b -> "%02x".format(b) }
        }
    }

    private fun java.sql.ResultSet.toSession(): Session =
        Session(
            id = getString("id"),
            userId = getString("user_id"),
            tokenHash = getString("token_hash"),
            createdAt = instant("created_at"),
            expiresAt = instant("expires_at"),
            revokedAt = getTimestamp("revoked_at")?.toInstant(),
        )
}

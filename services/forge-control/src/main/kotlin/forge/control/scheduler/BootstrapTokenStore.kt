package forge.control.scheduler

import forge.control.http.ApiException
import forge.control.repo.runSql
import forge.control.repo.withConnection
import java.security.MessageDigest
import java.security.SecureRandom
import java.sql.Timestamp
import java.time.Instant
import java.util.Base64
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import javax.sql.DataSource

data class BootstrapTokenRecord(
    val id: String,
    val organization: String,
    val nodePool: String?,
    val expiresAt: Instant,
    val consumedAt: Instant? = null,
    val consumedByNode: String? = null,
    val revokedAt: Instant? = null,
    val createdAt: Instant,
)

data class IssuedBootstrapToken(
    val record: BootstrapTokenRecord,
    /** Opaque plaintext; returned once. Never persist. */
    val plaintext: String,
)

sealed class BootstrapTokenVerifyResult {
    data class Ok(val record: BootstrapTokenRecord) : BootstrapTokenVerifyResult()
    data class Invalid(val reason: String) : BootstrapTokenVerifyResult()
}

interface BootstrapTokenStore {
    fun issue(
        organization: String,
        nodePool: String? = null,
        ttlSeconds: Long = 900,
        now: Instant = Instant.now(),
    ): IssuedBootstrapToken

    fun verify(plaintext: String, now: Instant = Instant.now()): BootstrapTokenVerifyResult

    /** Atomically consume an unconsumed, unrevoked, unexpired token for [nodeId]. */
    fun consume(
        plaintext: String,
        nodeId: String,
        now: Instant = Instant.now(),
    ): BootstrapTokenRecord

    fun revoke(tokenId: String, now: Instant = Instant.now()): BootstrapTokenRecord?

    fun find(tokenId: String): BootstrapTokenRecord?
}

class JdbcBootstrapTokenStore(
    private val dataSource: DataSource,
    private val defaultTtlSeconds: Long = 900,
    private val random: SecureRandom = SecureRandom(),
) : BootstrapTokenStore {
    override fun issue(
        organization: String,
        nodePool: String?,
        ttlSeconds: Long,
        now: Instant,
    ): IssuedBootstrapToken {
        val org = organization.trim()
        if (org.isEmpty()) {
            throw ApiException.BadRequest(
                "organization is required",
                mapOf("field" to "organization"),
            )
        }
        val ttl = if (ttlSeconds > 0) ttlSeconds else defaultTtlSeconds
        val id = "bst_${UUID.randomUUID().toString().replace("-", "").take(16)}"
        val plaintext = generatePlaintext()
        val hash = hashToken(plaintext)
        val expiresAt = now.plusSeconds(ttl)
        val pool = nodePool?.trim()?.takeIf { it.isNotEmpty() }
        return runSql {
            dataSource.withConnection { conn ->
                conn.prepareStatement(
                    """
                    INSERT INTO bootstrap_tokens (
                        id, token_hash, organization, node_pool, expires_at, created_at
                    ) VALUES (?, ?, ?, ?, ?, ?)
                    """.trimIndent(),
                ).use { ps ->
                    ps.setString(1, id)
                    ps.setString(2, hash)
                    ps.setString(3, org)
                    ps.setString(4, pool)
                    ps.setTimestamp(5, Timestamp.from(expiresAt))
                    ps.setTimestamp(6, Timestamp.from(now))
                    ps.executeUpdate()
                }
                IssuedBootstrapToken(
                    record = BootstrapTokenRecord(
                        id = id,
                        organization = org,
                        nodePool = pool,
                        expiresAt = expiresAt,
                        createdAt = now,
                    ),
                    plaintext = plaintext,
                )
            }
        }
    }

    override fun verify(plaintext: String, now: Instant): BootstrapTokenVerifyResult {
        val token = plaintext.trim()
        if (token.isEmpty() || !looksLikeBootstrapToken(token)) {
            return BootstrapTokenVerifyResult.Invalid("malformed")
        }
        val record = findByHash(hashToken(token))
            ?: return BootstrapTokenVerifyResult.Invalid("unknown")
        return classify(record, now)
    }

    override fun consume(
        plaintext: String,
        nodeId: String,
        now: Instant,
    ): BootstrapTokenRecord {
        val verified = verify(plaintext, now)
        if (verified is BootstrapTokenVerifyResult.Invalid) {
            throw invalidBootstrapToken(verified.reason)
        }
        val record = (verified as BootstrapTokenVerifyResult.Ok).record
        val node = nodeId.trim()
        if (node.isEmpty()) {
            throw ApiException.BadRequest("node_id is required", mapOf("field" to "node_id"))
        }
        return runSql {
            dataSource.withConnection { conn ->
                val updated = conn.prepareStatement(
                    """
                    UPDATE bootstrap_tokens
                    SET consumed_at = ?, consumed_by_node = ?
                    WHERE id = ?
                      AND consumed_at IS NULL
                      AND revoked_at IS NULL
                      AND expires_at > ?
                    """.trimIndent(),
                ).use { ps ->
                    ps.setTimestamp(1, Timestamp.from(now))
                    ps.setString(2, node)
                    ps.setString(3, record.id)
                    ps.setTimestamp(4, Timestamp.from(now))
                    ps.executeUpdate()
                }
                if (updated != 1) {
                    throw invalidBootstrapToken("already consumed")
                }
                find(conn, record.id) ?: error("bootstrap token missing after consume")
            }
        }
    }

    override fun revoke(tokenId: String, now: Instant): BootstrapTokenRecord? = runSql {
        dataSource.withConnection { conn ->
            val id = tokenId.trim()
            if (id.isEmpty()) return@withConnection null
            conn.prepareStatement(
                """
                UPDATE bootstrap_tokens
                SET revoked_at = COALESCE(revoked_at, ?)
                WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setTimestamp(1, Timestamp.from(now))
                ps.setString(2, id)
                if (ps.executeUpdate() == 0) return@withConnection null
            }
            find(conn, id)
        }
    }

    override fun find(tokenId: String): BootstrapTokenRecord? = runSql {
        dataSource.withConnection { conn -> find(conn, tokenId.trim()) }
    }

    private fun findByHash(hash: String): BootstrapTokenRecord? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, organization, node_pool, expires_at, consumed_at,
                       consumed_by_node, revoked_at, created_at
                FROM bootstrap_tokens
                WHERE token_hash = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, hash)
                ps.executeQuery().use { rs ->
                    if (!rs.next()) return@withConnection null
                    mapRow(rs)
                }
            }
        }
    }

    private fun find(conn: java.sql.Connection, id: String): BootstrapTokenRecord? {
        conn.prepareStatement(
            """
            SELECT id, organization, node_pool, expires_at, consumed_at,
                   consumed_by_node, revoked_at, created_at
            FROM bootstrap_tokens
            WHERE id = ?
            """.trimIndent(),
        ).use { ps ->
            ps.setString(1, id)
            ps.executeQuery().use { rs ->
                if (!rs.next()) return null
                return mapRow(rs)
            }
        }
    }

    private fun mapRow(rs: java.sql.ResultSet): BootstrapTokenRecord =
        BootstrapTokenRecord(
            id = rs.getString("id"),
            organization = rs.getString("organization"),
            nodePool = rs.getString("node_pool"),
            expiresAt = rs.getTimestamp("expires_at").toInstant(),
            consumedAt = rs.getTimestamp("consumed_at")?.toInstant(),
            consumedByNode = rs.getString("consumed_by_node"),
            revokedAt = rs.getTimestamp("revoked_at")?.toInstant(),
            createdAt = rs.getTimestamp("created_at").toInstant(),
        )

    private fun generatePlaintext(): String {
        val bytes = ByteArray(24)
        random.nextBytes(bytes)
        val encoded = Base64.getUrlEncoder().withoutPadding().encodeToString(bytes)
        return "bst_$encoded"
    }

    companion object {
        fun hashToken(token: String): String {
            val digest = MessageDigest.getInstance("SHA-256").digest(token.toByteArray(Charsets.UTF_8))
            return digest.joinToString("") { b -> "%02x".format(b) }
        }

        fun looksLikeBootstrapToken(token: String): Boolean = token.startsWith("bst_")

        fun classify(record: BootstrapTokenRecord, now: Instant): BootstrapTokenVerifyResult {
            if (record.revokedAt != null) {
                return BootstrapTokenVerifyResult.Invalid("revoked")
            }
            if (record.consumedAt != null) {
                return BootstrapTokenVerifyResult.Invalid("already consumed")
            }
            if (!record.expiresAt.isAfter(now)) {
                return BootstrapTokenVerifyResult.Invalid("expired")
            }
            return BootstrapTokenVerifyResult.Ok(record)
        }

        fun invalidBootstrapToken(reason: String): ApiException =
            ApiException.Unauthorized(
                "invalid bootstrap token",
                details = mapOf("reason" to reason),
                code = "InvalidBootstrapToken",
            )
    }
}

/** In-memory store for unit tests. */
class InMemoryBootstrapTokenStore(
    private val defaultTtlSeconds: Long = 900,
    private val random: SecureRandom = SecureRandom(),
) : BootstrapTokenStore {
    private val byId = ConcurrentHashMap<String, Pair<BootstrapTokenRecord, String>>()
    private val hashToId = ConcurrentHashMap<String, String>()

    override fun issue(
        organization: String,
        nodePool: String?,
        ttlSeconds: Long,
        now: Instant,
    ): IssuedBootstrapToken {
        val org = organization.trim()
        if (org.isEmpty()) {
            throw ApiException.BadRequest(
                "organization is required",
                mapOf("field" to "organization"),
            )
        }
        val ttl = if (ttlSeconds > 0) ttlSeconds else defaultTtlSeconds
        val id = "bst_${UUID.randomUUID().toString().replace("-", "").take(16)}"
        val bytes = ByteArray(24)
        random.nextBytes(bytes)
        val plaintext = "bst_" + Base64.getUrlEncoder().withoutPadding().encodeToString(bytes)
        val record = BootstrapTokenRecord(
            id = id,
            organization = org,
            nodePool = nodePool?.trim()?.takeIf { it.isNotEmpty() },
            expiresAt = now.plusSeconds(ttl),
            createdAt = now,
        )
        byId[id] = record to plaintext
        hashToId[JdbcBootstrapTokenStore.hashToken(plaintext)] = id
        return IssuedBootstrapToken(record, plaintext)
    }

    override fun verify(plaintext: String, now: Instant): BootstrapTokenVerifyResult {
        val token = plaintext.trim()
        if (token.isEmpty() || !JdbcBootstrapTokenStore.looksLikeBootstrapToken(token)) {
            return BootstrapTokenVerifyResult.Invalid("malformed")
        }
        val id = hashToId[JdbcBootstrapTokenStore.hashToken(token)]
            ?: return BootstrapTokenVerifyResult.Invalid("unknown")
        val record = byId[id]?.first ?: return BootstrapTokenVerifyResult.Invalid("unknown")
        return JdbcBootstrapTokenStore.classify(record, now)
    }

    override fun consume(
        plaintext: String,
        nodeId: String,
        now: Instant,
    ): BootstrapTokenRecord {
        synchronized(byId) {
            val verified = verify(plaintext, now)
            if (verified is BootstrapTokenVerifyResult.Invalid) {
                throw JdbcBootstrapTokenStore.invalidBootstrapToken(verified.reason)
            }
            val record = (verified as BootstrapTokenVerifyResult.Ok).record
            val current = byId[record.id] ?: throw JdbcBootstrapTokenStore.invalidBootstrapToken("unknown")
            if (current.first.consumedAt != null || current.first.revokedAt != null) {
                throw JdbcBootstrapTokenStore.invalidBootstrapToken("already consumed")
            }
            val updated = current.first.copy(consumedAt = now, consumedByNode = nodeId.trim())
            byId[record.id] = updated to current.second
            return updated
        }
    }

    override fun revoke(tokenId: String, now: Instant): BootstrapTokenRecord? {
        val current = byId[tokenId.trim()] ?: return null
        val updated = current.first.copy(revokedAt = current.first.revokedAt ?: now)
        byId[tokenId.trim()] = updated to current.second
        return updated
    }

    override fun find(tokenId: String): BootstrapTokenRecord? = byId[tokenId.trim()]?.first
}

package forge.identity.token

import forge.identity.authz.PrincipalRef
import forge.identity.authz.Role
import forge.identity.authz.RoleResolver
import forge.identity.config.TokenConfig
import forge.identity.db.StoreException
import forge.identity.db.instant
import forge.identity.db.runSql
import forge.identity.db.withConnection
import forge.identity.http.ApiException
import forge.identity.logging.JsonLog
import forge.identity.metrics.IdentityMetrics
import forge.identity.org.OrgStore
import forge.identity.project.ProjectMembershipStore
import forge.identity.user.UserStore
import java.security.MessageDigest
import java.security.SecureRandom
import java.sql.Timestamp
import java.time.Instant
import java.util.Base64
import java.util.UUID
import javax.sql.DataSource

data class ApiToken(
    val id: String,
    val prefix: String,
    val tokenHash: String,
    val ownerType: String,
    val ownerId: String,
    val projectId: String,
    val role: String,
    val createdAt: Instant,
    val expiresAt: Instant?,
    val revokedAt: Instant?,
) {
    fun isActive(now: Instant = Instant.now()): Boolean {
        if (revokedAt != null) return false
        if (expiresAt != null && !expiresAt.isAfter(now)) return false
        return true
    }
}

data class CreatedToken(
    val token: ApiToken,
    /** Opaque plaintext; returned once. Never persist. */
    val plaintext: String,
)

class TokenStore(
    private val dataSource: DataSource,
    private val tokenConfig: TokenConfig,
    private val projects: ProjectMembershipStore,
    private val users: UserStore,
    private val serviceAccounts: ServiceAccountStore,
    private val roleResolver: RoleResolver,
    private val log: JsonLog? = null,
    private val random: SecureRandom = SecureRandom(),
) {
    constructor(
        dataSource: DataSource,
        tokenConfig: TokenConfig,
        projects: ProjectMembershipStore,
        users: UserStore,
        orgs: OrgStore,
        serviceAccounts: ServiceAccountStore,
        log: JsonLog? = null,
        random: SecureRandom = SecureRandom(),
    ) : this(
        dataSource = dataSource,
        tokenConfig = tokenConfig,
        projects = projects,
        users = users,
        serviceAccounts = serviceAccounts,
        roleResolver = RoleResolver(projects, orgs),
        log = log,
        random = random,
    )

    fun create(
        ownerType: String,
        ownerId: String,
        projectId: String,
        role: String,
        expiresInSeconds: Long? = null,
        now: Instant = Instant.now(),
    ): CreatedToken {
        val type = ownerType.trim()
        val oid = ownerId.trim()
        val pid = projectId.trim()
        val normalizedRole = role.trim()
        if (type !in setOf("user", "service_account")) {
            throw ApiException.BadRequest(
                "owner.type must be user or service_account",
                mapOf("field" to "owner.type"),
            )
        }
        if (oid.isEmpty()) {
            throw ApiException.BadRequest("owner.id must not be blank", mapOf("field" to "owner.id"))
        }
        if (pid.isEmpty()) {
            throw ApiException.BadRequest("project_id must not be blank", mapOf("field" to "project_id"))
        }
        if (normalizedRole.isEmpty()) {
            throw ApiException.BadRequest("role must not be blank", mapOf("field" to "role"))
        }
        val parsedRole = Role.fromWire(normalizedRole)
            ?: throw ApiException.BadRequest(
                "role must be a known membership role",
                mapOf("field" to "role", "role" to normalizedRole),
            )
        if (parsedRole !in Role.membershipRoles) {
            throw ApiException.BadRequest(
                "role must be a known membership role",
                mapOf("field" to "role", "role" to normalizedRole),
            )
        }
        projects.getProject(pid)
        enforceOwnerMembership(type, oid, pid)

        val ttl = expiresInSeconds ?: tokenConfig.defaultTtlSeconds
        if (ttl != null && ttl < 1) {
            throw ApiException.BadRequest(
                "expires_in_s must be a positive integer when set",
                mapOf("field" to "expires_in_s"),
            )
        }
        val expiresAt = ttl?.let { now.plusSeconds(it) }

        val id = UUID.randomUUID().toString()
        val plaintext = generateOpaqueToken(type)
        val tokenHash = hashToken(plaintext)
        val prefixLen = tokenConfig.prefixLen.coerceIn(1, plaintext.length)
        val prefix = plaintext.take(prefixLen)

        try {
            runSql {
                dataSource.withConnection { conn ->
                    conn.prepareStatement(
                        """
                        INSERT INTO api_tokens (
                          id, prefix, token_hash, owner_type, owner_id, project_id, role,
                          created_at, expires_at, revoked_at
                        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
                        """.trimIndent(),
                    ).use { ps ->
                        ps.setString(1, id)
                        ps.setString(2, prefix)
                        ps.setString(3, tokenHash)
                        ps.setString(4, type)
                        ps.setString(5, oid)
                        ps.setString(6, pid)
                        ps.setString(7, normalizedRole)
                        ps.setTimestamp(8, Timestamp.from(now))
                        if (expiresAt == null) {
                            ps.setTimestamp(9, null)
                        } else {
                            ps.setTimestamp(9, Timestamp.from(expiresAt))
                        }
                        ps.executeUpdate()
                    }
                }
            }
        } catch (_: StoreException.Conflict) {
            throw ApiException.Conflict("token hash collision; retry", mapOf("token_id" to id))
        } catch (_: StoreException.ConstraintViolation) {
            throw ApiException.NotFound("project not found", mapOf("project_id" to pid))
        }

        IdentityMetrics.recordTokenCreated()
        IdentityMetrics.setActiveTokens(countActive())
        log?.info(
            "token created",
            "token_id" to id,
            "prefix" to prefix,
            "owner_type" to type,
            "owner_id" to oid,
            "project_id" to pid,
            "role" to normalizedRole,
        )
        val record = ApiToken(
            id = id,
            prefix = prefix,
            tokenHash = tokenHash,
            ownerType = type,
            ownerId = oid,
            projectId = pid,
            role = normalizedRole,
            createdAt = now,
            expiresAt = expiresAt,
            revokedAt = null,
        )
        return CreatedToken(record, plaintext)
    }

    fun listByOwner(ownerId: String, ownerType: String? = null): List<ApiToken> {
        val oid = ownerId.trim()
        if (oid.isEmpty()) {
            throw ApiException.BadRequest("owner must not be blank", mapOf("field" to "owner"))
        }
        val type = ownerType?.trim()?.ifEmpty { null }
        if (type != null && type !in setOf("user", "service_account")) {
            throw ApiException.BadRequest(
                "owner_type must be user or service_account",
                mapOf("field" to "owner_type"),
            )
        }
        return runSql {
            dataSource.withConnection { conn ->
                val sql = if (type != null) {
                    """
                    SELECT id, prefix, token_hash, owner_type, owner_id, project_id, role,
                           created_at, expires_at, revoked_at
                    FROM api_tokens
                    WHERE owner_id = ? AND owner_type = ?
                    ORDER BY created_at DESC, id
                    """.trimIndent()
                } else {
                    """
                    SELECT id, prefix, token_hash, owner_type, owner_id, project_id, role,
                           created_at, expires_at, revoked_at
                    FROM api_tokens
                    WHERE owner_id = ?
                    ORDER BY created_at DESC, id
                    """.trimIndent()
                }
                conn.prepareStatement(sql).use { ps ->
                    ps.setString(1, oid)
                    if (type != null) ps.setString(2, type)
                    ps.executeQuery().use { rs ->
                        buildList {
                            while (rs.next()) add(rs.toApiToken())
                        }
                    }
                }
            }
        }
    }

    fun findByToken(token: String): ApiToken? {
        if (token.isBlank()) return null
        return findByTokenHash(hashToken(token))
    }

    fun findByTokenHash(tokenHash: String): ApiToken? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, prefix, token_hash, owner_type, owner_id, project_id, role,
                       created_at, expires_at, revoked_at
                FROM api_tokens WHERE token_hash = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, tokenHash)
                ps.executeQuery().use { rs ->
                    if (!rs.next()) null else rs.toApiToken()
                }
            }
        }
    }

    fun findById(id: String): ApiToken? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, prefix, token_hash, owner_type, owner_id, project_id, role,
                       created_at, expires_at, revoked_at
                FROM api_tokens WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, id)
                ps.executeQuery().use { rs ->
                    if (!rs.next()) null else rs.toApiToken()
                }
            }
        }
    }

    /** Revoke by id. Idempotent; returns false when unknown. */
    fun revoke(tokenId: String, now: Instant = Instant.now()): Boolean {
        val id = tokenId.trim()
        if (id.isEmpty()) {
            throw ApiException.BadRequest("token id must not be blank", mapOf("field" to "id"))
        }
        val existing = findById(id)
            ?: throw ApiException.NotFound("token not found", mapOf("token_id" to id))
        if (existing.revokedAt != null) {
            return true
        }
        runSql {
            dataSource.withConnection { conn ->
                conn.prepareStatement(
                    """
                    UPDATE api_tokens
                    SET revoked_at = ?
                    WHERE id = ? AND revoked_at IS NULL
                    """.trimIndent(),
                ).use { ps ->
                    ps.setTimestamp(1, Timestamp.from(now))
                    ps.setString(2, id)
                    ps.executeUpdate()
                }
            }
        }
        IdentityMetrics.recordTokenRevoked()
        IdentityMetrics.setActiveTokens(countActive())
        log?.info(
            "token revoked",
            "token_id" to id,
            "prefix" to existing.prefix,
            "owner_type" to existing.ownerType,
            "owner_id" to existing.ownerId,
            "project_id" to existing.projectId,
            "role" to existing.role,
        )
        return true
    }

    fun countActive(now: Instant = Instant.now()): Long = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT COUNT(*) AS c
                FROM api_tokens
                WHERE revoked_at IS NULL
                  AND (expires_at IS NULL OR expires_at > ?)
                """.trimIndent(),
            ).use { ps ->
                ps.setTimestamp(1, Timestamp.from(now))
                ps.executeQuery().use { rs ->
                    if (!rs.next()) 0L else rs.getLong("c")
                }
            }
        }
    }

    private fun enforceOwnerMembership(ownerType: String, ownerId: String, projectId: String) {
        when (ownerType) {
            "user" -> {
                if (!users.exists(ownerId)) {
                    throw ApiException.NotFound("user not found", mapOf("user_id" to ownerId))
                }
                val effective = roleResolver.resolve(PrincipalRef("user", ownerId), projectId)
                if (effective == Role.NONE) {
                    throw ApiException.Forbidden(
                        "owner is not a member of the project",
                        mapOf("owner_id" to ownerId, "project_id" to projectId),
                    )
                }
            }
            "service_account" -> {
                val sa = serviceAccounts.findById(ownerId)
                    ?: throw ApiException.NotFound(
                        "service account not found",
                        mapOf("service_account_id" to ownerId),
                    )
                if (sa.projectId != projectId) {
                    throw ApiException.Forbidden(
                        "service account does not belong to the project",
                        mapOf("owner_id" to ownerId, "project_id" to projectId),
                    )
                }
            }
        }
    }

    private fun generateOpaqueToken(ownerType: String): String {
        val typePrefix = when (ownerType) {
            "user" -> "forge_pat_"
            "service_account" -> "forge_sat_"
            else -> error("unexpected owner type $ownerType")
        }
        val bytes = ByteArray(32)
        random.nextBytes(bytes)
        val secret = Base64.getUrlEncoder().withoutPadding().encodeToString(bytes)
        return typePrefix + secret
    }

    companion object {
        fun hashToken(token: String): String {
            val digest = MessageDigest.getInstance("SHA-256").digest(token.toByteArray(Charsets.UTF_8))
            return digest.joinToString("") { b -> "%02x".format(b) }
        }

        fun looksLikeApiToken(token: String): Boolean =
            token.startsWith("forge_pat_") || token.startsWith("forge_sat_")
    }

    private fun java.sql.ResultSet.toApiToken(): ApiToken =
        ApiToken(
            id = getString("id"),
            prefix = getString("prefix"),
            tokenHash = getString("token_hash"),
            ownerType = getString("owner_type"),
            ownerId = getString("owner_id"),
            projectId = getString("project_id"),
            role = getString("role"),
            createdAt = instant("created_at"),
            expiresAt = getTimestamp("expires_at")?.toInstant(),
            revokedAt = getTimestamp("revoked_at")?.toInstant(),
        )
}

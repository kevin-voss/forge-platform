package forge.control.scheduler

import forge.control.http.ApiException
import forge.control.repo.instant
import forge.control.repo.runSql
import forge.control.repo.withConnection
import java.sql.Timestamp
import java.time.Instant
import java.util.concurrent.ConcurrentHashMap
import javax.sql.DataSource

enum class PreemptionPolicy {
    Never,
    PreemptLowerPriority,
    ;

    companion object {
        fun parse(raw: String?): PreemptionPolicy =
            when (raw?.trim()) {
                null, "", "Never" -> Never
                "PreemptLowerPriority" -> PreemptLowerPriority
                else -> throw IllegalArgumentException(
                    "preemption_policy must be Never|PreemptLowerPriority, got '$raw'",
                )
            }
    }

    fun wire(): String = name
}

data class PriorityClass(
    val name: String,
    val value: Int,
    val preemptionPolicy: PreemptionPolicy = PreemptionPolicy.Never,
    val description: String? = null,
    val createdAt: Instant = Instant.EPOCH,
)

interface PriorityClassStore {
    fun ensureDefault()

    fun create(
        name: String,
        value: Int,
        preemptionPolicy: PreemptionPolicy,
        description: String? = null,
        now: Instant = Instant.now(),
    ): PriorityClass

    fun find(name: String): PriorityClass?

    fun list(): List<PriorityClass>

    /** Resolve [name] or the configured default; never null after [ensureDefault]. */
    fun resolve(name: String?): PriorityClass
}

class JdbcPriorityClassStore(
    private val dataSource: DataSource,
    private val defaultName: String = DEFAULT_NAME,
) : PriorityClassStore {
    override fun ensureDefault() {
        runSql {
            dataSource.withConnection { conn ->
                conn.prepareStatement(
                    """
                    INSERT INTO priority_classes(name, value, preemption_policy, description, created_at)
                    VALUES (?, 0, 'Never', ?, ?)
                    ON CONFLICT (name) DO NOTHING
                    """.trimIndent(),
                ).use { ps ->
                    ps.setString(1, defaultName)
                    ps.setString(2, "Implicit class for placements created before epic 25")
                    ps.setTimestamp(3, Timestamp.from(Instant.EPOCH))
                    ps.executeUpdate()
                }
            }
        }
    }

    override fun create(
        name: String,
        value: Int,
        preemptionPolicy: PreemptionPolicy,
        description: String?,
        now: Instant,
    ): PriorityClass {
        val trimmed = name.trim()
        if (trimmed.isEmpty()) {
            throw ApiException.BadRequest("name is required", mapOf("field" to "name"))
        }
        if (trimmed == defaultName) {
            throw ApiException.Conflict(
                "priority class '$defaultName' is reserved",
                mapOf("name" to defaultName),
                code = "priority_class_reserved",
            )
        }
        return runSql {
            dataSource.withConnection { conn ->
                conn.prepareStatement(
                    """
                    INSERT INTO priority_classes(name, value, preemption_policy, description, created_at)
                    VALUES (?, ?, ?, ?, ?)
                    """.trimIndent(),
                ).use { ps ->
                    ps.setString(1, trimmed)
                    ps.setInt(2, value)
                    ps.setString(3, preemptionPolicy.wire())
                    ps.setString(4, description?.trim()?.takeIf { it.isNotEmpty() })
                    ps.setTimestamp(5, Timestamp.from(now))
                    try {
                        ps.executeUpdate()
                    } catch (e: java.sql.SQLException) {
                        if (e.sqlState == "23505") {
                            throw ApiException.Conflict(
                                "priority class already exists",
                                mapOf("name" to trimmed),
                                code = "priority_class_exists",
                            )
                        }
                        throw e
                    }
                }
                find(trimmed) ?: error("priority class missing after insert")
            }
        }
    }

    override fun find(name: String): PriorityClass? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT name, value, preemption_policy, description, created_at
                FROM priority_classes
                WHERE name = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, name.trim())
                ps.executeQuery().use { rs ->
                    if (!rs.next()) return@withConnection null
                    read(rs)
                }
            }
        }
    }

    override fun list(): List<PriorityClass> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT name, value, preemption_policy, description, created_at
                FROM priority_classes
                ORDER BY value DESC, name ASC
                """.trimIndent(),
            ).use { ps ->
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) add(read(rs))
                    }
                }
            }
        }
    }

    override fun resolve(name: String?): PriorityClass {
        val key = name?.trim()?.takeIf { it.isNotEmpty() } ?: defaultName
        return find(key)
            ?: find(defaultName)
            ?: PriorityClass(name = defaultName, value = 0, preemptionPolicy = PreemptionPolicy.Never)
    }

    private fun read(rs: java.sql.ResultSet): PriorityClass =
        PriorityClass(
            name = rs.getString("name"),
            value = rs.getInt("value"),
            preemptionPolicy = PreemptionPolicy.parse(rs.getString("preemption_policy")),
            description = rs.getString("description"),
            createdAt = rs.instant("created_at"),
        )

    companion object {
        const val DEFAULT_NAME: String = "default"
    }
}

/** In-memory store for unit tests; seeds [default] on construction. */
class InMemoryPriorityClassStore(
    private val defaultName: String = JdbcPriorityClassStore.DEFAULT_NAME,
) : PriorityClassStore {
    private val rows = ConcurrentHashMap<String, PriorityClass>()

    init {
        ensureDefault()
    }

    override fun ensureDefault() {
        rows.putIfAbsent(
            defaultName,
            PriorityClass(
                name = defaultName,
                value = 0,
                preemptionPolicy = PreemptionPolicy.Never,
                description = "Implicit class for placements created before epic 25",
                createdAt = Instant.EPOCH,
            ),
        )
    }

    override fun create(
        name: String,
        value: Int,
        preemptionPolicy: PreemptionPolicy,
        description: String?,
        now: Instant,
    ): PriorityClass {
        val trimmed = name.trim()
        if (trimmed.isEmpty()) {
            throw ApiException.BadRequest("name is required", mapOf("field" to "name"))
        }
        if (trimmed == defaultName) {
            throw ApiException.Conflict(
                "priority class '$defaultName' is reserved",
                mapOf("name" to defaultName),
                code = "priority_class_reserved",
            )
        }
        val created = PriorityClass(
            name = trimmed,
            value = value,
            preemptionPolicy = preemptionPolicy,
            description = description?.trim()?.takeIf { it.isNotEmpty() },
            createdAt = now,
        )
        if (rows.putIfAbsent(trimmed, created) != null) {
            throw ApiException.Conflict(
                "priority class already exists",
                mapOf("name" to trimmed),
                code = "priority_class_exists",
            )
        }
        return created
    }

    override fun find(name: String): PriorityClass? = rows[name.trim()]

    override fun list(): List<PriorityClass> =
        rows.values.sortedWith(compareByDescending<PriorityClass> { it.value }.thenBy { it.name })

    override fun resolve(name: String?): PriorityClass {
        val key = name?.trim()?.takeIf { it.isNotEmpty() } ?: defaultName
        return find(key)
            ?: find(defaultName)
            ?: PriorityClass(name = defaultName, value = 0, preemptionPolicy = PreemptionPolicy.Never)
    }
}

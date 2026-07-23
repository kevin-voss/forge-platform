package forge.control.manageddb

import java.sql.DriverManager
import java.sql.SQLException
import java.util.Properties
import java.util.concurrent.TimeUnit

/** Admin operations against a product Postgres instance (never Control's DB). */
interface PostgresAdminClient {
    fun waitReady(timeoutMs: Long = 60_000, pollMs: Long = 500)
    fun ping(user: String, password: String, database: String): Boolean
    fun createDatabaseAndRole(databaseName: String, roleName: String, rolePassword: String): List<String>
    fun createRoleOnDatabase(databaseName: String, roleName: String, rolePassword: String): List<String>
    fun revokeRole(roleName: String, reassignTo: String? = null)
    fun dropDatabaseAndRole(databaseName: String, roleName: String)
    fun dropDatabase(databaseName: String, roleNames: List<String> = emptyList())
}

/**
 * Admin JDBC helpers for product Postgres instances.
 * Builds least-privilege roles scoped to a single database.
 */
class PostgresAdmin(
    private val host: String,
    private val port: Int,
    private val adminUser: String = "postgres",
    private val adminPassword: String,
) : PostgresAdminClient {
    override fun waitReady(timeoutMs: Long, pollMs: Long) {
        val deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(timeoutMs)
        var last: Exception? = null
        while (System.nanoTime() < deadline) {
            try {
                ping(adminUser, adminPassword, "postgres")
                return
            } catch (e: Exception) {
                last = e
                Thread.sleep(pollMs)
            }
        }
        throw PostgresAdminException(
            "postgres not ready at $host:$port within ${timeoutMs}ms: ${last?.message ?: "unknown"}",
        )
    }

    override fun ping(user: String, password: String, database: String): Boolean {
        connect(user, password, database).use { conn ->
            conn.createStatement().use { st ->
                st.executeQuery("SELECT 1").use { rs ->
                    return rs.next()
                }
            }
        }
    }

    /**
     * Create database + login role with privileges only on that database.
     * Returns the SQL statements applied (for unit assertions; no secrets).
     */
    override fun createDatabaseAndRole(
        databaseName: String,
        roleName: String,
        rolePassword: String,
    ): List<String> {
        validateIdent(databaseName, "database")
        validateIdent(roleName, "role")
        val statements = RoleGrantSql.plan(databaseName, roleName)
        connect(adminUser, adminPassword, "postgres").use { conn ->
            conn.autoCommit = true
            conn.createStatement().use { st ->
                st.execute(RoleGrantSql.createRole(roleName, rolePassword))
                st.execute(RoleGrantSql.createDatabase(databaseName, roleName))
                st.execute(RoleGrantSql.revokePublicConnect(databaseName))
                st.execute(RoleGrantSql.grantConnect(databaseName, roleName))
            }
        }
        connect(adminUser, adminPassword, databaseName).use { conn ->
            conn.autoCommit = true
            conn.createStatement().use { st ->
                st.execute(RoleGrantSql.grantSchemaPrivileges(roleName))
            }
        }
        return statements
    }

    /**
     * Create an additional login role with privileges on an existing database
     * (credential rotation). Does not create a new database.
     */
    override fun createRoleOnDatabase(
        databaseName: String,
        roleName: String,
        rolePassword: String,
    ): List<String> {
        validateIdent(databaseName, "database")
        validateIdent(roleName, "role")
        val statements = listOf(
            RoleGrantSql.createRole(roleName, "<redacted>"),
            RoleGrantSql.grantConnect(databaseName, roleName),
            RoleGrantSql.grantSchemaPrivileges(roleName),
            RoleGrantSql.grantAllTables(roleName),
            RoleGrantSql.alterDefaultPrivileges(roleName),
        )
        connect(adminUser, adminPassword, "postgres").use { conn ->
            conn.autoCommit = true
            conn.createStatement().use { st ->
                st.execute(RoleGrantSql.createRole(roleName, rolePassword))
                st.execute(RoleGrantSql.grantConnect(databaseName, roleName))
            }
        }
        connect(adminUser, adminPassword, databaseName).use { conn ->
            conn.autoCommit = true
            conn.createStatement().use { st ->
                st.execute(RoleGrantSql.grantSchemaPrivileges(roleName))
                st.execute(RoleGrantSql.grantAllTables(roleName))
                st.execute(RoleGrantSql.alterDefaultPrivileges(roleName))
            }
        }
        return statements
    }

    override fun revokeRole(roleName: String, reassignTo: String?) {
        if (!isSafeIdent(roleName)) return
        if (reassignTo != null && !isSafeIdent(reassignTo)) return
        try {
            connect(adminUser, adminPassword, "postgres").use { conn ->
                conn.autoCommit = true
                conn.createStatement().use { st ->
                    if (reassignTo != null) {
                        // Best-effort ownership transfer across databases.
                        st.execute("REASSIGN OWNED BY \"$roleName\" TO \"$reassignTo\"")
                        st.execute("DROP OWNED BY \"$roleName\"")
                    }
                    st.execute("DROP ROLE IF EXISTS \"$roleName\"")
                }
            }
        } catch (_: SQLException) {
            try {
                connect(adminUser, adminPassword, "postgres").use { conn ->
                    conn.autoCommit = true
                    conn.createStatement().use { st ->
                        st.execute("ALTER ROLE \"$roleName\" NOLOGIN")
                    }
                }
            } catch (_: SQLException) {
                // best-effort invalidate
            }
        }
    }

    override fun dropDatabaseAndRole(databaseName: String, roleName: String) {
        dropDatabase(databaseName, listOf(roleName))
    }

    override fun dropDatabase(databaseName: String, roleNames: List<String>) {
        if (!isSafeIdent(databaseName)) return
        try {
            connect(adminUser, adminPassword, "postgres").use { conn ->
                conn.autoCommit = true
                conn.createStatement().use { st ->
                    st.execute(
                        "SELECT pg_terminate_backend(pid) FROM pg_stat_activity " +
                            "WHERE datname = '$databaseName' AND pid <> pg_backend_pid()",
                    )
                    st.execute("DROP DATABASE IF EXISTS \"$databaseName\"")
                    for (roleName in roleNames) {
                        if (!isSafeIdent(roleName)) continue
                        try {
                            st.execute("DROP ROLE IF EXISTS \"$roleName\"")
                        } catch (_: SQLException) {
                            // continue
                        }
                    }
                }
            }
        } catch (_: SQLException) {
            // best-effort rollback
        }
    }

    private fun connect(user: String, password: String, database: String) =
        DriverManager.getConnection(
            "jdbc:postgresql://$host:$port/$database",
            Properties().apply {
                setProperty("user", user)
                setProperty("password", password)
                setProperty("loginTimeout", "5")
                setProperty("connectTimeout", "5")
                setProperty("socketTimeout", "15")
            },
        )

    companion object {
        fun validateIdent(value: String, kind: String) {
            if (!isSafeIdent(value)) {
                throw PostgresAdminException("invalid $kind identifier: $value")
            }
        }

        fun isSafeIdent(value: String): Boolean =
            value.matches(Regex("^[a-z_][a-z0-9_]{0,62}$"))
    }
}

/** Pure SQL planner for least-privilege grants (unit-testable, no secrets). */
object RoleGrantSql {
    fun plan(databaseName: String, roleName: String): List<String> =
        listOf(
            createRole(roleName, "<redacted>"),
            createDatabase(databaseName, roleName),
            revokePublicConnect(databaseName),
            grantConnect(databaseName, roleName),
            grantSchemaPrivileges(roleName),
        )

    fun createRole(roleName: String, password: String): String =
        "CREATE ROLE \"$roleName\" WITH LOGIN PASSWORD '${password.replace("'", "''")}'"

    fun createDatabase(databaseName: String, roleName: String): String =
        "CREATE DATABASE \"$databaseName\" OWNER \"$roleName\""

    fun revokePublicConnect(databaseName: String): String =
        "REVOKE CONNECT ON DATABASE \"$databaseName\" FROM PUBLIC"

    fun grantConnect(databaseName: String, roleName: String): String =
        "GRANT CONNECT ON DATABASE \"$databaseName\" TO \"$roleName\""

    fun grantSchemaPrivileges(roleName: String): String =
        "GRANT ALL ON SCHEMA public TO \"$roleName\""

    fun grantAllTables(roleName: String): String =
        "GRANT ALL ON ALL TABLES IN SCHEMA public TO \"$roleName\""

    fun alterDefaultPrivileges(roleName: String): String =
        "ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO \"$roleName\""

    /** True when grants mention only the target database (no other DB CONNECT grants). */
    fun isLimitedToDatabase(statements: List<String>, databaseName: String): Boolean {
        val connectGrants = statements.filter { it.startsWith("GRANT CONNECT ON DATABASE") }
        return connectGrants.size == 1 &&
            connectGrants.single().contains("\"$databaseName\"") &&
            statements.none { it.contains("SUPERUSER") } &&
            statements.none { it.contains("CREATEDB") } &&
            statements.none { it.contains("CREATEROLE") }
    }
}

class PostgresAdminException(message: String) : RuntimeException(message)

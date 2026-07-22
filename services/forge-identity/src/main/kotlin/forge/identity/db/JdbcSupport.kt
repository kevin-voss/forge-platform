package forge.identity.db

import java.sql.ResultSet
import java.sql.SQLException
import java.sql.Timestamp
import java.time.Instant
import javax.sql.DataSource

internal fun ResultSet.instant(column: String): Instant {
    val ts: Timestamp = getTimestamp(column)
        ?: error("null timestamp for column $column")
    return ts.toInstant()
}

internal inline fun <T> DataSource.withConnection(block: (java.sql.Connection) -> T): T =
    connection.use(block)

internal fun mapSqlException(e: SQLException): StoreException {
    val state = e.sqlState.orEmpty()
    val msg = e.message ?: e.javaClass.simpleName
    return when (state) {
        "23505" -> StoreException.Conflict("unique constraint violated: $msg", e)
        "23503" -> StoreException.ConstraintViolation("foreign key violation: $msg", e)
        "23514" -> StoreException.ConstraintViolation("check constraint violated: $msg", e)
        else -> StoreException.ConstraintViolation("database constraint error: $msg", e)
    }
}

/** Run a SQL block, mapping SQLSTATE uniqueness/FK failures to [StoreException]. */
internal fun <T> runSql(block: () -> T): T =
    try {
        block()
    } catch (e: SQLException) {
        throw mapSqlException(e)
    }

package forge.control.repo

import forge.control.telemetry.Telemetry
import java.sql.ResultSet
import java.sql.SQLException
import java.sql.Timestamp
import java.time.Instant
import java.util.UUID
import javax.sql.DataSource

internal fun ResultSet.uuid(column: String): UUID = getObject(column, UUID::class.java)

internal fun ResultSet.instant(column: String): Instant {
    val ts: Timestamp = getTimestamp(column)
        ?: error("null timestamp for column $column")
    return ts.toInstant()
}

internal inline fun <T> DataSource.withConnection(block: (java.sql.Connection) -> T): T =
    connection.use(block)

internal fun mapSqlException(e: SQLException): RepositoryException {
    val state = e.sqlState.orEmpty()
    val msg = e.message ?: e.javaClass.simpleName
    return when (state) {
        "23505" -> RepositoryException.Conflict("unique constraint violated: $msg", e)
        "23503" -> RepositoryException.ConstraintViolation("foreign key violation: $msg", e)
        "23514" -> RepositoryException.ConstraintViolation("check constraint violated: $msg", e)
        else -> RepositoryException.ConstraintViolation("database constraint error: $msg", e)
    }
}

internal fun <T> runSql(block: () -> T): T =
    Telemetry.current().inSpan("db.query") {
        try {
            block()
        } catch (e: SQLException) {
            throw mapSqlException(e)
        }
    }

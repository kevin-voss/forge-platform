package forge.control.repo

import java.util.UUID

/** Typed persistence errors for the API layer (02.04/02.06) to translate. */
sealed class RepositoryException(
    message: String,
    cause: Throwable? = null,
) : Exception(message, cause) {
    class NotFound(
        val entity: String,
        val id: UUID,
    ) : RepositoryException("$entity not found: $id")

    class Conflict(
        message: String,
        cause: Throwable? = null,
    ) : RepositoryException(message, cause)

    class ConstraintViolation(
        message: String,
        cause: Throwable? = null,
    ) : RepositoryException(message, cause)
}

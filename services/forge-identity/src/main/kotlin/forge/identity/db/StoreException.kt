package forge.identity.db

/** Typed persistence errors for the API layer to translate. */
sealed class StoreException(
    message: String,
    cause: Throwable? = null,
) : Exception(message, cause) {
    class NotFound(
        val entity: String,
        val id: String,
    ) : StoreException("$entity not found: $id")

    class Conflict(
        message: String,
        cause: Throwable? = null,
    ) : StoreException(message, cause)

    class ConstraintViolation(
        message: String,
        cause: Throwable? = null,
    ) : StoreException(message, cause)
}

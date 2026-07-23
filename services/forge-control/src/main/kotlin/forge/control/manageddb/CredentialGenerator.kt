package forge.control.manageddb

import java.security.SecureRandom

/**
 * Strong random credentials for product DB roles.
 * Passwords are never logged; Control persists only Secrets references.
 */
object CredentialGenerator {
    private val random = SecureRandom()
    private val alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

    /** URL-safe-ish password with high entropy (default 32 chars ≈ 190 bits). */
    fun password(length: Int = 32): String {
        require(length >= 16) { "password length must be >= 16" }
        val bytes = ByteArray(length)
        random.nextBytes(bytes)
        return buildString(length) {
            for (b in bytes) {
                append(alphabet[(b.toInt() and 0xff) % alphabet.length])
            }
        }
    }

    /** Postgres-safe role name derived from database name (+ short suffix for uniqueness). */
    fun username(databaseName: String, suffix: String = ""): String {
        val base = databaseName.lowercase()
            .replace(Regex("[^a-z0-9_]"), "_")
            .trim('_')
            .ifBlank { "app" }
        val role = if (suffix.isBlank()) "${base}_user" else "${base}_$suffix"
        val clipped = role.take(63)
        return if (clipped.first().isDigit()) "u_$clipped".take(63) else clipped
    }

    fun isStrongPassword(value: String): Boolean =
        value.length >= 16 && value.all { it in alphabet }
}

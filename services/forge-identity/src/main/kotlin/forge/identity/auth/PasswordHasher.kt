package forge.identity.auth

import de.mkammerer.argon2.Argon2Factory

/**
 * Argon2id password hashing with configurable memory/iterations.
 * Encoded hashes include salt + params (PHC string format).
 */
class PasswordHasher(
    private val memoryKb: Int = 65_536,
    private val iterations: Int = 3,
    private val parallelism: Int = 1,
    private val hashLength: Int = 32,
    private val saltLength: Int = 16,
) {
    private val argon2 = Argon2Factory.create(
        Argon2Factory.Argon2Types.ARGON2id,
        saltLength,
        hashLength,
    )

    fun hash(password: String): String {
        require(password.isNotEmpty()) { "password must not be empty" }
        val chars = password.toCharArray()
        return try {
            argon2.hash(iterations, memoryKb, parallelism, chars)
        } finally {
            argon2.wipeArray(chars)
        }
    }

    fun verify(encodedHash: String, password: String): Boolean {
        if (encodedHash.isBlank() || password.isEmpty()) return false
        val chars = password.toCharArray()
        return try {
            argon2.verify(encodedHash, chars)
        } catch (_: Exception) {
            false
        } finally {
            argon2.wipeArray(chars)
        }
    }
}

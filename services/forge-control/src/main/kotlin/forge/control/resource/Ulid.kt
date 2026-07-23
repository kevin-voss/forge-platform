package forge.control.resource

import java.security.SecureRandom
import java.util.concurrent.locks.ReentrantLock
import kotlin.concurrent.withLock

/**
 * 128-bit ULID generator (48-bit ms timestamp + 80-bit randomness) encoded as
 * Crockford Base32. No third-party dependency.
 *
 * Output shape: `{prefix}_{26-char body}`, e.g. `app_01J5Z3K9QDJ8XN5V2H9T3RXYA`.
 * Consecutive calls within the same millisecond are strictly monotonic.
 */
object Ulid {
    private const val ALPHABET = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
    private const val BODY_LENGTH = 26
    private const val RANDOM_BITS = 80

    private val lock = ReentrantLock()
    private val random = SecureRandom()
    private var lastTimestampMs: Long = -1L
    private var lastRandom: ByteArray = ByteArray(10)

    fun next(prefix: String): String {
        require(prefix.isNotBlank()) { "prefix must not be blank" }
        val body = lock.withLock { nextBodyLocked() }
        return "${prefix}_$body"
    }

    private fun nextBodyLocked(): String {
        var now = System.currentTimeMillis()
        if (now == lastTimestampMs) {
            if (!incrementRandom(lastRandom)) {
                // Random overflow within the same ms — wait for the next millisecond.
                while (now == lastTimestampMs) {
                    now = System.currentTimeMillis()
                }
                fillRandom(lastRandom)
                lastTimestampMs = now
            }
        } else {
            fillRandom(lastRandom)
            lastTimestampMs = now
        }
        return encode(lastTimestampMs, lastRandom)
    }

    private fun fillRandom(target: ByteArray) {
        random.nextBytes(target)
    }

    /** Big-endian increment of 80-bit randomness; returns false on overflow. */
    private fun incrementRandom(bytes: ByteArray): Boolean {
        for (i in bytes.indices.reversed()) {
            val next = (bytes[i].toInt() and 0xff) + 1
            bytes[i] = (next and 0xff).toByte()
            if (next <= 0xff) return true
        }
        return false
    }

    private fun encode(timestampMs: Long, randomness: ByteArray): String {
        require(randomness.size == 10) { "expected 10 random bytes ($RANDOM_BITS bits)" }
        // 128 bits → 26 Crockford chars (each char is 5 bits; last char uses 3 leftover bits).
        val chars = CharArray(BODY_LENGTH)
        // Timestamp: 48 bits → 10 chars
        var ts = timestampMs and 0xFFFF_FFFF_FFFFL
        for (i in 9 downTo 0) {
            chars[i] = ALPHABET[(ts and 0x1f).toInt()]
            ts = ts ushr 5
        }
        // Randomness: 80 bits → 16 chars
        var acc = 0
        var bits = 0
        var out = 10
        for (b in randomness) {
            acc = (acc shl 8) or (b.toInt() and 0xff)
            bits += 8
            while (bits >= 5) {
                bits -= 5
                chars[out++] = ALPHABET[(acc ushr bits) and 0x1f]
            }
        }
        if (bits > 0) {
            chars[out] = ALPHABET[(acc shl (5 - bits)) and 0x1f]
        }
        return String(chars)
    }
}

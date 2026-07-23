package forge.control.manageddb

import java.security.MessageDigest

/** SHA-256 helpers for managed-db backup archives. */
object BackupChecksum {
    fun sha256Hex(bytes: ByteArray): String =
        MessageDigest.getInstance("SHA-256")
            .digest(bytes)
            .joinToString("") { "%02x".format(it) }

    fun verify(bytes: ByteArray, expectedHex: String): Boolean {
        val expected = expectedHex.trim().lowercase()
        if (expected.isEmpty()) return false
        return sha256Hex(bytes) == expected
    }
}

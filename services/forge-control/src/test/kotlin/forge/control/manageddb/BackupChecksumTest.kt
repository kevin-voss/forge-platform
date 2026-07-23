package forge.control.manageddb

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class BackupChecksumTest {
    @Test
    fun computesAndVerifiesSha256() {
        val bytes = "fixture-row-1".toByteArray()
        val checksum = BackupChecksum.sha256Hex(bytes)
        assertEquals(64, checksum.length)
        assertTrue(BackupChecksum.verify(bytes, checksum))
        assertTrue(BackupChecksum.verify(bytes, checksum.uppercase()))
        assertFalse(BackupChecksum.verify(bytes, "0".repeat(64)))
        assertFalse(BackupChecksum.verify(bytes, ""))
    }
}

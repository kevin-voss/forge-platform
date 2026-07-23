package forge.control.scheduler

import forge.control.http.ApiException
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertFailsWith
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class BootstrapTokenStoreTest {
    @Test
    fun issueVerifySucceedsOnceSecondFailsConsumed() {
        val store = InMemoryBootstrapTokenStore()
        val t0 = Instant.parse("2026-07-23T10:00:00Z")
        val issued = store.issue("forge-labs", "np-default", 900, t0)
        assertTrue(issued.plaintext.startsWith("bst_"))

        val first = store.verify(issued.plaintext, t0.plusSeconds(1))
        assertIs<BootstrapTokenVerifyResult.Ok>(first)

        store.consume(issued.plaintext, "node-a", t0.plusSeconds(2))
        val second = store.verify(issued.plaintext, t0.plusSeconds(3))
        assertIs<BootstrapTokenVerifyResult.Invalid>(second)
        assertEquals("already consumed", second.reason)

        val err = assertFailsWith<ApiException.Unauthorized> {
            store.consume(issued.plaintext, "node-b", t0.plusSeconds(4))
        }
        assertEquals("InvalidBootstrapToken", err.code)
    }

    @Test
    fun expiredTokenFails() {
        val store = InMemoryBootstrapTokenStore()
        val t0 = Instant.parse("2026-07-23T10:00:00Z")
        val issued = store.issue("forge-labs", null, 60, t0)
        val result = store.verify(issued.plaintext, t0.plusSeconds(120))
        assertIs<BootstrapTokenVerifyResult.Invalid>(result)
        assertEquals("expired", result.reason)
    }

    @Test
    fun revokedTokenFails() {
        val store = InMemoryBootstrapTokenStore()
        val t0 = Instant.parse("2026-07-23T10:00:00Z")
        val issued = store.issue("forge-labs", null, 900, t0)
        assertNotNull(store.revoke(issued.record.id, t0.plusSeconds(1)))
        val result = store.verify(issued.plaintext, t0.plusSeconds(2))
        assertIs<BootstrapTokenVerifyResult.Invalid>(result)
        assertEquals("revoked", result.reason)
        assertNull(store.revoke("missing", t0))
    }
}

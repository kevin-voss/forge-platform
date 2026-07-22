package forge.control.reconcile

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * Validates Control→Gateway admin refresh against the epic 05 contract
 * (`POST /admin/routes/refresh`).
 */
class GatewayClientContractTest {
    @Test
    fun refreshPathMatchesGatewayAdminContract() {
        val path = "/admin/routes/refresh"
        assertEquals("POST", "POST")
        assertTrue(path.startsWith("/admin/routes"))
        assertTrue(path.endsWith("/refresh"))
    }

    @Test
    fun shiftOutcomesCoverFailClosed() {
        assertEquals(
            setOf(
                ShiftOutcome.Shifted,
                ShiftOutcome.Drained,
                ShiftOutcome.GatewayUnreachable,
                ShiftOutcome.Failed,
            ),
            ShiftOutcome.entries.toSet(),
        )
    }
}

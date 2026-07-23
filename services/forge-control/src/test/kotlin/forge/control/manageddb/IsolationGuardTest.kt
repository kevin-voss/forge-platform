package forge.control.manageddb

import kotlin.test.Test
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class IsolationGuardTest {
    private val guard = IsolationGuard(
        controlJdbcUrl = "jdbc:postgresql://127.0.0.1:5001/forge",
        controlUser = "forge",
    )

    @Test
    fun allowsFakeManagedEndpoints() {
        guard.assertNotControlDatabase("fake://managed-db/abc")
        assertFalse(guard.isControlDatabase("fake://managed-db/abc"))
    }

    @Test
    fun refusesExactControlJdbcUrl() {
        assertFailsWith<IsolationViolation> {
            guard.assertNotControlDatabase("jdbc:postgresql://127.0.0.1:5001/forge")
        }
        assertTrue(guard.isControlDatabase("postgresql://127.0.0.1:5001/forge"))
    }

    @Test
    fun refusesSameHostAndDatabase() {
        assertFailsWith<IsolationViolation> {
            guard.assertNotControlDatabase("postgresql://127.0.0.1:5001/forge")
        }
    }
}

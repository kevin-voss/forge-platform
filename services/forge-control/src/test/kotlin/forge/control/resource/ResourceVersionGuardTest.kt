package forge.control.resource

import forge.control.http.ApiException
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

class ResourceVersionGuardTest {
    @Test
    fun passesOnMatch() {
        ResourceVersionGuard.checkMatch(expected = 42L, current = 42L)
    }

    @Test
    fun throwsTypedConflictOnMismatch() {
        val ex = assertFailsWith<ApiException.Conflict> {
            ResourceVersionGuard.checkMatch(expected = 1040L, current = 1042L)
        }
        assertEquals("resource_version_conflict", ex.code)
        assertEquals("1042", ex.details?.get("currentResourceVersion"))
        assertEquals("resourceVersion 1040 is stale", ex.message)
    }

    @Test
    fun acceptCreateIsNoOp() {
        ResourceVersionGuard.acceptCreate()
    }
}

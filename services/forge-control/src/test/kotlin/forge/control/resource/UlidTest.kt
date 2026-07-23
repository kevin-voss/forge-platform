package forge.control.resource

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class UlidTest {
    private val crockford = Regex("^[0-9A-HJKMNP-TV-Z]{26}$")

    @Test
    fun nextReturnsPrefixedCrockfordBody() {
        val id = Ulid.next("app")
        assertTrue(id.startsWith("app_"), "expected app_ prefix, got $id")
        val body = id.removePrefix("app_")
        assertEquals(26, body.length)
        assertTrue(crockford.matches(body), "body not Crockford Base32: $body")
    }

    @Test
    fun consecutiveCallsAreStrictlyIncreasing() {
        val ids = (1..1000).map { Ulid.next("app") }
        for (i in 1 until ids.size) {
            assertTrue(
                ids[i] > ids[i - 1],
                "expected strictly increasing: ${ids[i - 1]} then ${ids[i]}",
            )
        }
    }
}

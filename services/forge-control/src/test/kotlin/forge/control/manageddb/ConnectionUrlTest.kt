package forge.control.manageddb

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class ConnectionUrlTest {
    @Test
    fun composesPostgresqlUrlFromParts() {
        val url = ConnectionUrl.compose(
            username = "app_user",
            password = "s3cret",
            host = "db.local",
            port = 5433,
            database = "appdb",
        )
        assertEquals("postgresql://app_user:s3cret@db.local:5433/appdb", url)
    }

    @Test
    fun percentEncodesSpecialCharactersInCredentials() {
        val url = ConnectionUrl.compose(
            username = "u@name",
            password = "p@ss:w/rd",
            host = "127.0.0.1",
            port = 5432,
            database = "db",
        )
        assertTrue(url.startsWith("postgresql://"))
        assertTrue(url.contains("u%40name"))
        assertTrue(url.contains("p%40ss%3Aw%2Frd"))
        assertTrue(url.endsWith("@127.0.0.1:5432/db"))
    }

    @Test
    fun rejectsBlankParts() {
        assertFailsWith<IllegalArgumentException> {
            ConnectionUrl.compose("", "pw", "h", 5432, "db")
        }
        assertFailsWith<IllegalArgumentException> {
            ConnectionUrl.compose("u", "pw", "", 5432, "db")
        }
        assertFailsWith<IllegalArgumentException> {
            ConnectionUrl.compose("u", "pw", "h", 0, "db")
        }
    }
}

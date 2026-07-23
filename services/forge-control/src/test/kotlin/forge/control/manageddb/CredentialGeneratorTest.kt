package forge.control.manageddb

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class CredentialGeneratorTest {
    @Test
    fun passwordHasSufficientEntropyAndSafeCharset() {
        val pw = CredentialGenerator.password(32)
        assertEquals(32, pw.length)
        assertTrue(CredentialGenerator.isStrongPassword(pw))
        val other = CredentialGenerator.password(32)
        assertTrue(pw != other, "passwords should be random")
    }

    @Test
    fun passwordRejectsShortLength() {
        assertFailsWith<IllegalArgumentException> {
            CredentialGenerator.password(8)
        }
    }

    @Test
    fun usernameIsPostgresSafe() {
        val user = CredentialGenerator.username("App-DB!", "ab12cd")
        assertTrue(PostgresAdmin.isSafeIdent(user))
        assertTrue(user.startsWith("app_db"))
        assertTrue(!user.contains("-"))
        assertTrue(user.length <= 63)
    }

    @Test
    fun usernamePrefixesLeadingDigit() {
        val user = CredentialGenerator.username("9lives", "x")
        assertTrue(user.startsWith("u_"))
        assertTrue(PostgresAdmin.isSafeIdent(user))
    }
}

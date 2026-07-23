package forge.control.manageddb

import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class RoleGrantSqlTest {
    @Test
    fun grantsAreLimitedToTargetDatabase() {
        val statements = RoleGrantSql.plan("appdb", "appdb_user")
        assertTrue(RoleGrantSql.isLimitedToDatabase(statements, "appdb"))
        assertTrue(statements.any { it.contains("REVOKE CONNECT ON DATABASE \"appdb\" FROM PUBLIC") })
        assertTrue(statements.any { it.contains("GRANT CONNECT ON DATABASE \"appdb\" TO \"appdb_user\"") })
        assertFalse(statements.any { it.contains("SUPERUSER") })
        assertFalse(statements.any { it.contains("GRANT CONNECT ON DATABASE \"other\"") })
    }
}

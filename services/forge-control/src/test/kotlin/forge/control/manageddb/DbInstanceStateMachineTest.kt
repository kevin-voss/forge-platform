package forge.control.manageddb

import kotlin.test.Test
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class DbInstanceStateMachineTest {
    @Test
    fun allowsValidTransitions() {
        assertTrue(DbInstanceStateMachine.canTransition(DbInstanceStatus.Provisioning, DbInstanceStatus.Available))
        assertTrue(DbInstanceStateMachine.canTransition(DbInstanceStatus.Provisioning, DbInstanceStatus.Error))
        assertTrue(DbInstanceStateMachine.canTransition(DbInstanceStatus.Available, DbInstanceStatus.Deleting))
        assertTrue(DbInstanceStateMachine.canTransition(DbInstanceStatus.Available, DbInstanceStatus.Error))
        assertTrue(DbInstanceStateMachine.canTransition(DbInstanceStatus.Error, DbInstanceStatus.Provisioning))
        assertTrue(DbInstanceStateMachine.canTransition(DbInstanceStatus.Error, DbInstanceStatus.Deleting))
    }

    @Test
    fun rejectsInvalidTransitions() {
        assertFalse(DbInstanceStateMachine.canTransition(DbInstanceStatus.Available, DbInstanceStatus.Provisioning))
        assertFalse(DbInstanceStateMachine.canTransition(DbInstanceStatus.Deleting, DbInstanceStatus.Available))
        assertFalse(DbInstanceStateMachine.canTransition(DbInstanceStatus.Provisioning, DbInstanceStatus.Deleting))
        assertFailsWith<IllegalStateException> {
            DbInstanceStateMachine.requireTransition(DbInstanceStatus.Deleting, DbInstanceStatus.Error)
        }
    }
}

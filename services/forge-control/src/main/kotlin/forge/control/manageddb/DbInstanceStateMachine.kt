package forge.control.manageddb

/**
 * Valid transitions for managed DB instance status.
 *
 * ```
 * provisioning → available | error
 * available    → deleting | error
 * error        → provisioning | deleting
 * deleting     → (gone / terminal; no further transitions)
 * ```
 */
object DbInstanceStateMachine {
    private val allowed: Map<DbInstanceStatus, Set<DbInstanceStatus>> = mapOf(
        DbInstanceStatus.Provisioning to setOf(DbInstanceStatus.Available, DbInstanceStatus.Error),
        DbInstanceStatus.Available to setOf(DbInstanceStatus.Deleting, DbInstanceStatus.Error),
        DbInstanceStatus.Error to setOf(DbInstanceStatus.Provisioning, DbInstanceStatus.Deleting),
        DbInstanceStatus.Deleting to emptySet(),
    )

    fun canTransition(from: DbInstanceStatus, to: DbInstanceStatus): Boolean =
        allowed[from]?.contains(to) == true

    fun requireTransition(from: DbInstanceStatus, to: DbInstanceStatus) {
        if (!canTransition(from, to)) {
            throw IllegalStateException(
                "invalid db instance transition: ${from.wire} → ${to.wire}",
            )
        }
    }
}

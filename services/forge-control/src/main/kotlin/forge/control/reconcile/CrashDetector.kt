package forge.control.reconcile

/**
 * Classifies observed replicas that occupy a desired slot but are not healthy.
 * Those slots need recreate (treated as missing capacity by the planner/reconciler).
 */
object CrashDetector {
    private val CRASHED = setOf(ReplicaStatus.Failed, ReplicaStatus.Stopped)

    fun needsRecreate(replica: ReplicaObservation): Boolean =
        replica.statusEnum() in CRASHED

    fun crashedReplicas(actual: ActualState): List<ReplicaObservation> =
        actual.replicas.filter { needsRecreate(it) }
}

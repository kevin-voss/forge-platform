package forge.control.scheduler.model

/**
 * Input to [forge.control.scheduler.Scheduler.place].
 * Keep free of Control HTTP/reconcile types (extract seam).
 */
data class PlacementRequest(
    val deploymentId: String,
    val replicaIndex: Int,
    val serviceId: String? = null,
    val requirements: ResourceRequirements = ResourceRequirements(),
    val antiAffinity: AntiAffinity = AntiAffinity.Soft,
) {
    init {
        require(deploymentId.isNotBlank()) { "deploymentId must not be blank" }
        require(replicaIndex >= 0) { "replicaIndex must be >= 0" }
        require(requirements.slots >= 1) { "requirements.slots must be >= 1" }
    }
}

data class ResourceRequirements(
    val slots: Int = 1,
    val cpuMillis: Int? = null,
    val memMb: Int? = null,
)

enum class AntiAffinity {
    Soft,
    Hard,
    ;

    companion object {
        fun parse(raw: String?): AntiAffinity =
            when (raw?.trim()?.lowercase()) {
                null, "", "soft" -> Soft
                "hard" -> Hard
                else -> throw IllegalArgumentException(
                    "anti_affinity must be soft|hard, got '$raw'",
                )
            }
    }
}

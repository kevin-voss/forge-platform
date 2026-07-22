package forge.control.reconcile

import java.util.UUID
import kotlinx.serialization.Serializable

@Serializable
data class RolloutPolicy(
    val batchSize: Int = 1,
    val timeoutSeconds: Int = 120,
) {
    init {
        require(batchSize >= 1) { "rollout.batchSize must be >= 1" }
        require(timeoutSeconds >= 1) { "rollout.timeoutSeconds must be >= 1" }
    }
}

/** Target image + replica count + rollout policy for one deployment. */
@Serializable
data class DesiredState(
    val deploymentId: String,
    val image: String,
    val replicas: Int,
    val rollout: RolloutPolicy = RolloutPolicy(),
) {
    init {
        require(deploymentId.isNotBlank()) { "deploymentId must not be blank" }
        require(image.isNotBlank()) { "image must not be blank" }
        require(replicas >= 0) { "replicas must be >= 0" }
    }

    companion object {
        fun of(
            deploymentId: UUID,
            image: String,
            replicas: Int,
            batchSize: Int = 1,
            timeoutSeconds: Int = 120,
        ): DesiredState =
            DesiredState(
                deploymentId = deploymentId.toString(),
                image = image,
                replicas = replicas,
                rollout = RolloutPolicy(batchSize = batchSize, timeoutSeconds = timeoutSeconds),
            )
    }
}

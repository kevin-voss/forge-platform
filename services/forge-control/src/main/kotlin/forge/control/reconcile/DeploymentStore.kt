package forge.control.reconcile

import forge.control.domain.Deployment
import forge.control.repo.DeploymentRepository
import java.util.UUID

/** Narrow desired-state seam for the reconciliation controller. */
interface DeploymentStore {
    fun listDesired(): List<DesiredState>
    fun findDesired(deploymentId: UUID): DesiredState?
}

class RepositoryDeploymentStore(
    private val deployments: DeploymentRepository,
) : DeploymentStore {
    override fun listDesired(): List<DesiredState> =
        deployments.listAll().map { it.toDesiredState() }

    override fun findDesired(deploymentId: UUID): DesiredState? =
        deployments.findById(deploymentId)?.toDesiredState()
}

fun Deployment.toDesiredState(): DesiredState =
    DesiredState.of(
        deploymentId = id,
        image = image,
        replicas = desiredReplicas,
        batchSize = rolloutBatchSize,
        timeoutSeconds = rolloutTimeoutSeconds,
    )

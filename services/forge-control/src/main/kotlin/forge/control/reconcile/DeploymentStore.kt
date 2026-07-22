package forge.control.reconcile

import forge.control.domain.Deployment
import forge.control.domain.Service
import forge.control.repo.DeploymentRepository
import forge.control.repo.ServiceRepository
import java.util.UUID

/** Narrow desired-state seam for the reconciliation controller. */
interface DeploymentStore {
    fun listDesired(): List<DesiredState>
    fun findDesired(deploymentId: UUID): DesiredState?
}

class RepositoryDeploymentStore(
    private val deployments: DeploymentRepository,
    private val services: ServiceRepository? = null,
    private val rolloutBatchSizeOverride: Int? = null,
) : DeploymentStore {
    override fun listDesired(): List<DesiredState> =
        deployments.listAll().map {
            it.toDesiredState(services?.findById(it.serviceId), rolloutBatchSizeOverride)
        }

    override fun findDesired(deploymentId: UUID): DesiredState? {
        val deployment = deployments.findById(deploymentId) ?: return null
        return deployment.toDesiredState(
            services?.findById(deployment.serviceId),
            rolloutBatchSizeOverride,
        )
    }
}

fun Deployment.toDesiredState(
    service: Service? = null,
    batchSizeOverride: Int? = null,
): DesiredState =
    DesiredState.of(
        deploymentId = id,
        image = image,
        replicas = desiredReplicas,
        batchSize = batchSizeOverride ?: rolloutBatchSize,
        timeoutSeconds = rolloutTimeoutSeconds,
        serviceId = serviceId,
        serviceSlug = service?.name ?: "svc",
        port = service?.port ?: 8080,
    )

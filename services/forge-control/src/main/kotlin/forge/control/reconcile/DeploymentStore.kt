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
    fun getStatus(deploymentId: UUID): String?
    fun setStatus(deploymentId: UUID, status: String)
    fun setDesiredImage(deploymentId: UUID, image: String)
}

class RepositoryDeploymentStore(
    private val deployments: DeploymentRepository,
    private val services: ServiceRepository? = null,
    private val rolloutBatchSizeOverride: Int? = null,
    private val rolloutTimeoutOverride: Int? = null,
) : DeploymentStore {
    override fun listDesired(): List<DesiredState> =
        deployments.listAll().map {
            it.toDesiredState(services?.findById(it.serviceId), rolloutBatchSizeOverride, rolloutTimeoutOverride)
        }

    override fun findDesired(deploymentId: UUID): DesiredState? {
        val deployment = deployments.findById(deploymentId) ?: return null
        return deployment.toDesiredState(
            services?.findById(deployment.serviceId),
            rolloutBatchSizeOverride,
            rolloutTimeoutOverride,
        )
    }

    override fun getStatus(deploymentId: UUID): String? =
        deployments.findById(deploymentId)?.status

    override fun setStatus(deploymentId: UUID, status: String) {
        deployments.update(deploymentId, status = status)
    }

    override fun setDesiredImage(deploymentId: UUID, image: String) {
        deployments.update(deploymentId, image = image)
    }
}

fun Deployment.toDesiredState(
    service: Service? = null,
    batchSizeOverride: Int? = null,
    timeoutOverride: Int? = null,
): DesiredState =
    DesiredState.of(
        deploymentId = id,
        image = image,
        replicas = desiredReplicas,
        batchSize = batchSizeOverride ?: rolloutBatchSize,
        timeoutSeconds = timeoutOverride ?: rolloutTimeoutSeconds,
        serviceId = serviceId,
        serviceSlug = service?.name ?: "svc",
        port = service?.port ?: 8080,
    )

package forge.control.reconcile

import forge.control.domain.Deployment
import forge.control.domain.Service
import forge.control.repo.ApplicationRepository
import forge.control.repo.DeploymentRepository
import forge.control.repo.EnvironmentRepository
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
    private val applications: ApplicationRepository? = null,
    private val environments: EnvironmentRepository? = null,
    private val rolloutBatchSizeOverride: Int? = null,
    private val rolloutTimeoutOverride: Int? = null,
) : DeploymentStore {
    override fun listDesired(): List<DesiredState> =
        deployments.listAll().map { toDesired(it) }

    override fun findDesired(deploymentId: UUID): DesiredState? {
        val deployment = deployments.findById(deploymentId) ?: return null
        return toDesired(deployment)
    }

    override fun getStatus(deploymentId: UUID): String? =
        deployments.findById(deploymentId)?.status

    override fun setStatus(deploymentId: UUID, status: String) {
        deployments.update(deploymentId, status = status)
    }

    override fun setDesiredImage(deploymentId: UUID, image: String) {
        deployments.update(deploymentId, image = image)
    }

    private fun toDesired(deployment: Deployment): DesiredState {
        val service = services?.findById(deployment.serviceId)
        val environment = environments?.findById(deployment.environmentId)
        val projectId = when {
            environment != null -> environment.projectId.toString()
            service != null && applications != null ->
                applications.findById(service.applicationId)?.projectId?.toString().orEmpty()
            else -> ""
        }
        return deployment.toDesiredState(
            service = service,
            batchSizeOverride = rolloutBatchSizeOverride,
            timeoutOverride = rolloutTimeoutOverride,
            projectId = projectId,
            environmentName = environment?.name.orEmpty(),
        )
    }
}

fun Deployment.toDesiredState(
    service: Service? = null,
    batchSizeOverride: Int? = null,
    timeoutOverride: Int? = null,
    projectId: String = "",
    environmentName: String = "",
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
        projectId = projectId,
        environmentName = environmentName,
    )

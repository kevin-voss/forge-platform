package forge.control

import forge.control.reconcile.DeploymentHistory
import forge.control.reconcile.DeploymentStore
import forge.control.reconcile.ReconcileStatusStore
import forge.control.reconcile.RuntimeClient
import forge.control.scheduler.PlacementService
import forge.control.service.ApplicationService
import forge.control.service.DeploymentService
import forge.control.service.EnvironmentService
import forge.control.service.ProjectService
import forge.control.service.ProjectTreeService
import forge.control.service.ServiceService
import forge.control.repo.IdempotencyStore

/** Wired domain services for HTTP routes (null in health-only unit tests). */
data class ControlServices(
    val projects: ProjectService,
    val environments: EnvironmentService,
    val applications: ApplicationService,
    val services: ServiceService,
    val deployments: DeploymentService,
    val projectTrees: ProjectTreeService,
    val idempotency: IdempotencyStore? = null,
    val deploymentStore: DeploymentStore? = null,
    val runtimeClient: RuntimeClient? = null,
    val reconcileStatusStore: ReconcileStatusStore? = null,
    val deploymentHistory: DeploymentHistory? = null,
    val placementService: PlacementService? = null,
)

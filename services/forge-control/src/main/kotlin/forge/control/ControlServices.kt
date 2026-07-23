package forge.control

import forge.control.manageddb.ManagedDbService
import forge.control.reconcile.DeploymentHistory
import forge.control.reconcile.DeploymentStore
import forge.control.reconcile.ReconcileStatusStore
import forge.control.reconcile.RuntimeClient
import forge.control.scheduler.BootstrapTokenStore
import forge.control.scheduler.NodeJoinOrchestrator
import forge.control.scheduler.NodeStore
import forge.control.scheduler.PlacementService
import forge.control.scheduler.TaintChangeHandler
import forge.control.service.ApplicationService
import forge.control.service.DeploymentService
import forge.control.service.EnvironmentService
import forge.control.service.ProjectService
import forge.control.service.ProjectTreeService
import forge.control.service.ServiceService
import forge.control.repo.IdempotencyStore
import forge.control.resource.KindRegistry
import forge.control.resource.ResourceEventRepository
import forge.control.resource.ResourceRepository

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
    val nodeStore: NodeStore? = null,
    val nodeStrictRegister: Boolean = false,
    /** Invoked after a successful node registration (capacity may have increased). */
    val onNodeRegistered: (() -> Unit)? = null,
    val taintChangeHandler: TaintChangeHandler? = null,
    val bootstrapTokenStore: BootstrapTokenStore? = null,
    val nodeJoinOrchestrator: NodeJoinOrchestrator? = null,
    val bootstrapTokenTtlSeconds: Long = 900,
    val managedDb: ManagedDbService? = null,
    val resources: ResourceRepository? = null,
    val resourceEvents: ResourceEventRepository? = null,
    val kindRegistry: KindRegistry? = null,
)

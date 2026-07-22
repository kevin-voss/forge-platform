package forge.control

import forge.control.service.ApplicationService
import forge.control.service.DeploymentService
import forge.control.service.EnvironmentService
import forge.control.service.ProjectService
import forge.control.service.ProjectTreeService
import forge.control.service.ServiceService

/** Wired domain services for HTTP routes (null in health-only unit tests). */
data class ControlServices(
    val projects: ProjectService,
    val environments: EnvironmentService,
    val applications: ApplicationService,
    val services: ServiceService,
    val deployments: DeploymentService,
    val projectTrees: ProjectTreeService,
)

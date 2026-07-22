package forge.control

import forge.control.service.EnvironmentService
import forge.control.service.ProjectService

/** Wired domain services for HTTP routes (null in health-only unit tests). */
data class ControlServices(
    val projects: ProjectService,
    val environments: EnvironmentService,
)

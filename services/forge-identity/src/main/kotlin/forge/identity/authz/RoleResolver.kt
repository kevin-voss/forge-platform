package forge.identity.authz

import forge.identity.http.ApiException
import forge.identity.org.OrgStore
import forge.identity.project.ProjectMembershipStore

data class PrincipalRef(
    val type: String,
    val id: String,
)

/**
 * Resolves the effective role for a principal in a project scope.
 *
 * Order: organization-owner (any project in their org) > project membership role > none.
 */
class RoleResolver(
    private val projects: ProjectMembershipStore,
    private val orgs: OrgStore,
) {
    fun resolve(principal: PrincipalRef, projectId: String): Role {
        val project = projects.findProject(projectId)
            ?: throw ApiException.NotFound("project not found", mapOf("project_id" to projectId))

        val principalId = principal.id.trim()
        if (principalId.isEmpty()) {
            throw ApiException.BadRequest(
                "principal.id must not be blank",
                mapOf("field" to "principal.id"),
            )
        }

        // Org-owner implies admin over every project in the org (bounded escalation).
        val orgMembership = orgs.findMembership(project.orgId, principalId)
        if (orgMembership != null &&
            Role.fromWire(orgMembership.role) == Role.ORGANIZATION_OWNER
        ) {
            return Role.ORGANIZATION_OWNER
        }

        val projectMembership = projects.findMembership(projectId, principalId) ?: return Role.NONE
        return Role.fromWire(projectMembership.role) ?: Role.NONE
    }
}

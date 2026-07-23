package forge.control.manageddb

import forge.control.http.ApiException
import forge.control.logging.JsonLog
import forge.control.repo.RepositoryException
import forge.control.service.ProjectService
import forge.control.service.RelationshipValidator
import forge.control.telemetry.Telemetry
import java.util.UUID

class ManagedDbService(
    private val store: ManagedDbRepository,
    private val provisioner: Provisioner,
    private val isolation: IsolationGuard,
    private val relationships: RelationshipValidator,
    private val log: JsonLog? = null,
    private val telemetry: Telemetry = Telemetry.current(),
) {
    fun createInstance(projectId: UUID, nameRaw: String?): DbInstance {
        relationships.requireProject(projectId)
        val name = ProjectService.validateName(nameRaw)
        val created = try {
            store.createInstance(projectId, name, status = DbInstanceStatus.Provisioning)
        } catch (e: RepositoryException.Conflict) {
            throw ApiException.Conflict(
                "database instance name already exists in project",
                mapOf("name" to name, "projectId" to projectId.toString()),
            )
        } catch (e: RepositoryException.ConstraintViolation) {
            relationships.requireProject(projectId)
            throw ApiException.BadRequest(e.message ?: "constraint violation")
        } catch (e: RepositoryException) {
            throw mapRepo(e)
        }
        telemetry.recordManagedDbInstance(DbInstanceStatus.Provisioning.wire)
        log?.info(
            "managed db instance created",
            "instance_id" to created.id,
            "project_id" to projectId,
            "name" to name,
            "status" to created.status.wire,
        )

        return try {
            val result = provisioner.createInstance(created.id, projectId, name)
            isolation.assertNotControlDatabase(result.endpointRef)
            transition(created, DbInstanceStatus.Available, endpointRef = result.endpointRef)
        } catch (e: IsolationViolation) {
            transition(created, DbInstanceStatus.Error, reason = e.message)
            throw ApiException.BadRequest(
                e.message ?: "isolation invariant violated",
                mapOf("instanceId" to created.id.toString()),
            )
        } catch (e: Exception) {
            val reason = e.message ?: e.javaClass.simpleName
            transition(created, DbInstanceStatus.Error, reason = reason)
            log?.error(
                "managed db provisioner failed",
                "instance_id" to created.id,
                "error" to reason,
            )
            // Return the error-state record so callers see the lifecycle; HTTP still 201
            // for record creation when FakeProvisioner is healthy. Real failures surface as error status.
            store.findInstanceById(created.id)
                ?: throw ApiException.NotFound(
                    "database instance not found",
                    mapOf("id" to created.id.toString()),
                )
        }
    }

    fun getInstance(id: UUID): DbInstance =
        store.findInstanceById(id)
            ?: throw ApiException.NotFound(
                "database instance not found",
                mapOf("id" to id.toString()),
            )

    fun listInstances(projectId: UUID): List<DbInstance> {
        relationships.requireProject(projectId)
        return store.listInstances(projectId)
    }

    fun listDatabases(instanceId: UUID): List<DbDatabase> {
        getInstance(instanceId)
        return store.listDatabases(instanceId)
    }

    /** Test/helper: refuse assigning Control's JDBC URL as a product endpoint. */
    fun assertEndpointAllowed(endpointRef: String?) {
        try {
            isolation.assertNotControlDatabase(endpointRef)
        } catch (e: IsolationViolation) {
            throw ApiException.BadRequest(
                e.message ?: "isolation invariant violated",
                mapOf("field" to "endpointRef"),
            )
        }
    }

    private fun transition(
        current: DbInstance,
        to: DbInstanceStatus,
        reason: String? = null,
        endpointRef: String? = null,
    ): DbInstance {
        DbInstanceStateMachine.requireTransition(current.status, to)
        if (endpointRef != null) {
            isolation.assertNotControlDatabase(endpointRef)
        }
        val updated = store.updateInstanceStatus(
            id = current.id,
            status = to,
            statusReason = reason,
            endpointRef = endpointRef,
        )
        telemetry.recordManagedDbInstance(to.wire)
        log?.info(
            "managed db instance status transition",
            "instance_id" to current.id,
            "from" to current.status.wire,
            "to" to to.wire,
            "reason" to reason,
        )
        return updated
    }

    private fun mapRepo(e: RepositoryException): ApiException =
        when (e) {
            is RepositoryException.Conflict -> ApiException.Conflict(e.message ?: "conflict")
            is RepositoryException.NotFound -> ApiException.NotFound(e.message ?: "not found")
            is RepositoryException.ConstraintViolation ->
                ApiException.BadRequest(e.message ?: "constraint violation")
        }
}

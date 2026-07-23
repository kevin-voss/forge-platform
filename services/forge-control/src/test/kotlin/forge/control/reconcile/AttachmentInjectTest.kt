package forge.control.reconcile

import forge.control.logging.JsonLog
import forge.control.manageddb.AttachmentEnvResult
import forge.control.manageddb.AttachmentEnvSource
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class AttachmentInjectTest {
    @Test
    fun startReplicaMergesAttachmentEnvIntoWorkload() {
        val runtime = RecordingRuntime()
        val secrets = FakeSecretsClient(
            ResolvedEnvBundle(
                env = mapOf("FEATURE_X" to "true"),
                versionFingerprint = "fp-base",
            ),
        )
        val appId = UUID.randomUUID().toString()
        val attachments = AttachmentEnvSource {
            if (it == appId) {
                AttachmentEnvResult.Ready(
                    env = mapOf("DATABASE_URL" to "postgresql://u:p@host:5432/db"),
                    fingerprint = "fp-attach",
                )
            } else {
                AttachmentEnvResult.Empty
            }
        }
        val reconciler = Reconciler(
            runtimeClient = runtime,
            log = JsonLog("forge-control-test", "error"),
            secretsClient = secrets,
            attachmentEnvSource = attachments,
        )
        val desired = DesiredState.of(
            deploymentId = UUID.randomUUID(),
            image = "registry.local/demo:v1",
            replicas = 1,
            serviceSlug = "demo",
            projectId = "prj_1",
            environmentName = "production",
            applicationId = appId,
        )
        val plan = computePlan(desired, ActualState())
        val results = reconciler.execute(desired, ActualState(), plan)
        assertEquals(ActionResult.Created, results.single().result)
        val env = runtime.lastEnsure!!.environment
        assertEquals("true", env["FEATURE_X"])
        assertEquals("postgresql://u:p@host:5432/db", env["DATABASE_URL"])
        assertTrue(runtime.lastEnsure!!.secretsFingerprint.contains("fp-attach"))
    }

    @Test
    fun missingAttachmentSecretHoldsDeploy() {
        val runtime = RecordingRuntime()
        val reconciler = Reconciler(
            runtimeClient = runtime,
            log = JsonLog("forge-control-test", "error"),
            secretsClient = NoOpSecretsClient,
            attachmentEnvSource = AttachmentEnvSource {
                AttachmentEnvResult.Hold("attachment_secret_missing:x")
            },
        )
        val desired = DesiredState.of(
            deploymentId = UUID.randomUUID(),
            image = "img:v1",
            replicas = 1,
            applicationId = UUID.randomUUID().toString(),
        )
        val results = reconciler.execute(desired, ActualState(), computePlan(desired, ActualState()))
        assertEquals(ActionResult.Held, results.single().result)
        assertTrue(results.single().detail!!.startsWith("managed_db_attach:"))
        assertEquals(0, runtime.createCalls.get())
    }

    @Test
    fun detachMeansEmptyAttachmentEnvOnNextDeploy() {
        val runtime = RecordingRuntime()
        var attached = true
        val reconciler = Reconciler(
            runtimeClient = runtime,
            log = JsonLog("forge-control-test", "error"),
            secretsClient = NoOpSecretsClient,
            attachmentEnvSource = AttachmentEnvSource {
                if (attached) {
                    AttachmentEnvResult.Ready(
                        env = mapOf("DATABASE_URL" to "postgresql://u:p@h:1/db"),
                        fingerprint = "a",
                    )
                } else {
                    AttachmentEnvResult.Empty
                }
            },
        )
        val desired = DesiredState.of(
            deploymentId = UUID.randomUUID(),
            image = "img:v1",
            replicas = 1,
            applicationId = UUID.randomUUID().toString(),
        )
        reconciler.execute(desired, ActualState(), computePlan(desired, ActualState()))
        assertEquals("postgresql://u:p@h:1/db", runtime.lastEnsure!!.environment["DATABASE_URL"])

        attached = false
        runtime.createCalls.set(0)
        runtime.lastEnsure = null
        // Simulate recreate after detach (empty actual → StartReplica again).
        reconciler.execute(desired, ActualState(), computePlan(desired, ActualState()))
        assertEquals(null, runtime.lastEnsure!!.environment["DATABASE_URL"])
    }

    private class FakeSecretsClient(
        private val bundle: ResolvedEnvBundle,
    ) : SecretsClient {
        override fun resolve(projectId: String, environment: String, service: String): SecretsResolveResult =
            SecretsResolveResult.Ok(bundle)
    }

    private class RecordingRuntime : RuntimeClient {
        val createCalls = AtomicInteger(0)
        var lastEnsure: WorkloadEnsureRequest? = null
        private val workloads = ConcurrentHashMap<String, String>()

        override fun loadActual(deploymentId: UUID): ActualState = ActualState()
        override fun findWorkload(runtimeDeploymentId: String): WorkloadHandle? = null
        override fun ensureWorkload(request: WorkloadEnsureRequest): EnsureOutcome {
            lastEnsure = request
            createCalls.incrementAndGet()
            val id = WorkloadNamer.runtimeDeploymentId(
                request.serviceSlug,
                request.deploymentId,
                request.replicaIndex,
            )
            workloads[id] = "running"
            return EnsureOutcome.Created
        }

        override fun stopWorkload(runtimeDeploymentId: String) {
            workloads.remove(runtimeDeploymentId)
        }
    }
}

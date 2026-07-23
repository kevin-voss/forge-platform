package forge.control.reconcile

import forge.control.logging.JsonLog
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class SecretsInjectTest {
    @Test
    fun startReplicaInjectsResolvedEnvAndFingerprint() {
        val runtime = RecordingRuntime()
        val secrets = FakeSecretsClient(
            ResolvedEnvBundle(
                env = mapOf("DATABASE_PASSWORD" to "pw1", "FEATURE_X" to "true"),
                versionFingerprint = "fp-v1",
            ),
        )
        val log = JsonLog("forge-control-test", "error")
        val reconciler = Reconciler(
            runtimeClient = runtime,
            log = log,
            secretsClient = secrets,
        )
        val deploymentId = UUID.randomUUID()
        val desired = DesiredState.of(
            deploymentId = deploymentId,
            image = "registry.local/demo:v1",
            replicas = 1,
            serviceSlug = "demo",
            projectId = "prj_1",
            environmentName = "production",
        )
        val plan = computePlan(desired, ActualState())
        val results = reconciler.execute(desired, ActualState(), plan)
        assertEquals(ActionResult.Created, results.single().result)
        val req = runtime.lastEnsure!!
        assertEquals("pw1", req.environment["DATABASE_PASSWORD"])
        assertEquals("true", req.environment["FEATURE_X"])
        assertEquals("fp-v1", req.secretsFingerprint)
        assertTrue(secrets.resolveCalls.get() >= 1)
    }

    @Test
    fun missingBoundSecretHoldsDeployWithoutCreate() {
        val runtime = RecordingRuntime()
        val secrets = FakeSecretsClient(missing = "DATABASE_PASSWORD")
        val log = JsonLog("forge-control-test", "error")
        val reconciler = Reconciler(
            runtimeClient = runtime,
            log = log,
            secretsClient = secrets,
        )
        val deploymentId = UUID.randomUUID()
        val desired = DesiredState.of(
            deploymentId = deploymentId,
            image = "registry.local/demo:v1",
            replicas = 1,
            serviceSlug = "demo",
            projectId = "prj_1",
            environmentName = "production",
        )
        val plan = computePlan(desired, ActualState())
        val results = reconciler.execute(desired, ActualState(), plan)
        assertEquals(ActionResult.Held, results.single().result)
        assertTrue(results.single().detail!!.startsWith("missing_secrets"))
        assertEquals(0, runtime.createCalls.get())
    }

    @Test
    fun fingerprintDriftNeedsRollingUpdate() {
        val desired = DesiredState.of(
            deploymentId = UUID.randomUUID(),
            image = "img:v1",
            replicas = 1,
            secretsFingerprint = "fp-new",
        )
        val actual = ActualState(
            replicas = listOf(
                ReplicaObservation(
                    replicaId = "0",
                    status = "ready",
                    replicaIndex = 0,
                    image = "img:v1",
                    secretsFingerprint = "fp-old",
                ),
            ),
        )
        assertTrue(needsRollingUpdate(desired, actual))
        assertTrue(!replicaMatchesDesired(actual.replicas.single(), desired))
    }

    private class FakeSecretsClient(
        private val bundle: ResolvedEnvBundle? = null,
        private val missing: String? = null,
    ) : SecretsClient {
        val resolveCalls = AtomicInteger(0)

        override fun resolve(projectId: String, environment: String, service: String): SecretsResolveResult {
            resolveCalls.incrementAndGet()
            if (missing != null) {
                return SecretsResolveResult.Missing("missing bound names: $missing")
            }
            return SecretsResolveResult.Ok(bundle!!)
        }
    }

    private class RecordingRuntime : RuntimeClient {
        val workloads = ConcurrentHashMap<String, String>()
        val createCalls = AtomicInteger(0)
        var lastEnsure: WorkloadEnsureRequest? = null

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

package forge.control.manageddb

import java.util.concurrent.TimeUnit

data class ContainerInfo(
    val id: String,
    val name: String,
)

/** Subset of Docker operations used by [LocalProvisioner]. */
interface DockerEngine {
    fun ensureNetwork(name: String)
    fun createAndStartPostgres(
        name: String,
        network: String,
        image: String,
        adminPassword: String,
        labels: Map<String, String> = emptyMap(),
    ): ContainerInfo

    fun removeContainer(idOrName: String)
    fun publishedPort(containerId: String, containerPort: Int = 5432): Int
    fun containerEnv(containerId: String): Map<String, String>
    fun containerRunning(containerId: String): Boolean
}

/**
 * Docker Engine via the `docker` CLI (ProcessBuilder).
 * Keeps Control free of a Docker SDK dependency while matching local/dev topology.
 */
class CliDockerEngine(
    private val dockerBin: String = System.getenv("DOCKER_BIN")?.trim()?.ifEmpty { null } ?: "docker",
    private val timeoutSeconds: Long = 120,
) : DockerEngine {
    override fun ensureNetwork(name: String) {
        val inspect = run(listOf("network", "inspect", name), allowFailure = true)
        if (inspect.exitCode == 0) return
        val created = run(listOf("network", "create", name), allowFailure = true)
        if (created.exitCode != 0 && !created.stderr.contains("already exists")) {
            throw DockerEngineException("failed to create docker network '$name': ${created.stderr}".trim())
        }
    }

    override fun createAndStartPostgres(
        name: String,
        network: String,
        image: String,
        adminPassword: String,
        labels: Map<String, String>,
    ): ContainerInfo {
        ensureNetwork(network)
        // Publish on loopback so host-side Control can health-check; network alias for in-net peers.
        val args = mutableListOf(
            "run", "-d",
            "--name", name,
            "--network", network,
            "--network-alias", name,
            "-e", "POSTGRES_PASSWORD=$adminPassword",
            "-p", "127.0.0.1::5432",
        )
        for ((k, v) in labels) {
            args += listOf("--label", "$k=$v")
        }
        args += image
        val result = run(args)
        if (result.exitCode != 0) {
            throw DockerEngineException(
                "docker run failed for image '$image': ${result.stderr.ifBlank { result.stdout }}".trim(),
            )
        }
        val id = result.stdout.trim()
        if (id.isBlank()) {
            throw DockerEngineException("docker run returned empty container id")
        }
        return ContainerInfo(id = id, name = name)
    }

    override fun removeContainer(idOrName: String) {
        run(listOf("rm", "-f", idOrName), allowFailure = true)
    }

    override fun publishedPort(containerId: String, containerPort: Int): Int {
        val result = run(listOf("port", containerId, "$containerPort/tcp"))
        if (result.exitCode != 0) {
            throw DockerEngineException("docker port failed: ${result.stderr}".trim())
        }
        // e.g. 127.0.0.1:32768
        val line = result.stdout.lineSequence().firstOrNull { it.isNotBlank() }
            ?: throw DockerEngineException("docker port returned empty mapping")
        val port = line.substringAfterLast(':').trim().toIntOrNull()
            ?: throw DockerEngineException("unable to parse published port from '$line'")
        return port
    }

    override fun containerEnv(containerId: String): Map<String, String> {
        val result = run(
            listOf(
                "inspect",
                "-f",
                "{{range .Config.Env}}{{println .}}{{end}}",
                containerId,
            ),
        )
        if (result.exitCode != 0) {
            throw DockerEngineException("docker inspect failed: ${result.stderr}".trim())
        }
        return result.stdout.lineSequence()
            .map { it.trim() }
            .filter { it.contains('=') }
            .associate { line ->
                val idx = line.indexOf('=')
                line.substring(0, idx) to line.substring(idx + 1)
            }
    }

    override fun containerRunning(containerId: String): Boolean {
        val result = run(
            listOf("inspect", "-f", "{{.State.Running}}", containerId),
            allowFailure = true,
        )
        return result.exitCode == 0 && result.stdout.trim().equals("true", ignoreCase = true)
    }

    private data class ExecResult(val exitCode: Int, val stdout: String, val stderr: String)

    private fun run(args: List<String>, allowFailure: Boolean = false): ExecResult {
        val pb = ProcessBuilder(listOf(dockerBin) + args)
            .redirectErrorStream(false)
        val process = try {
            pb.start()
        } catch (e: Exception) {
            throw DockerEngineException("failed to start docker CLI: ${e.message ?: e.javaClass.simpleName}")
        }
        val stdout = process.inputStream.bufferedReader().readText()
        val stderr = process.errorStream.bufferedReader().readText()
        val finished = process.waitFor(timeoutSeconds, TimeUnit.SECONDS)
        if (!finished) {
            process.destroyForcibly()
            throw DockerEngineException("docker command timed out: ${args.joinToString(" ")}")
        }
        val code = process.exitValue()
        if (!allowFailure && code != 0) {
            return ExecResult(code, stdout, stderr)
        }
        return ExecResult(code, stdout, stderr)
    }
}

class DockerEngineException(message: String) : RuntimeException(message)

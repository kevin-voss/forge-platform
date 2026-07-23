package forge.control.manageddb

import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.util.UUID

/** Durable archive storage for managed-db backups (volume or Forge Storage). */
interface ArchiveStore {
    /** Persist [bytes] for [backupId]; returns opaque location reference. */
    fun put(projectId: UUID, backupId: UUID, bytes: ByteArray): String

    /** Fetch archive bytes for [location]; null if missing. */
    fun get(projectId: UUID, location: String): ByteArray?

    /** Best-effort delete of a partial/failed archive. */
    fun delete(projectId: UUID, location: String)
}

class VolumeArchiveStore(
    private val root: Path,
) : ArchiveStore {
    init {
        Files.createDirectories(root)
    }

    override fun put(projectId: UUID, backupId: UUID, bytes: ByteArray): String {
        val dir = root.resolve(projectId.toString())
        Files.createDirectories(dir)
        val file = dir.resolve("$backupId.dump")
        Files.write(file, bytes)
        return "volume://db-backups/$projectId/$backupId.dump"
    }

    override fun get(projectId: UUID, location: String): ByteArray? {
        val file = resolveVolumePath(location) ?: return null
        if (!Files.isRegularFile(file)) return null
        return Files.readAllBytes(file)
    }

    override fun delete(projectId: UUID, location: String) {
        val file = resolveVolumePath(location) ?: return
        try {
            Files.deleteIfExists(file)
        } catch (_: Exception) {
            // best effort
        }
    }

    private fun resolveVolumePath(location: String): Path? {
        if (!location.startsWith("volume://db-backups/")) return null
        val relative = location.removePrefix("volume://db-backups/")
        val path = root.resolve(relative).normalize()
        if (!path.startsWith(root.normalize())) return null
        return path
    }
}

/**
 * Forge Storage-backed archive store. Puts/gets under bucket/key
 * `db-backups/<backupId>.dump`. Falls back errors as [ArchiveStoreException].
 */
class StorageArchiveStore(
    private val storageUrl: String,
    private val bucket: String = "db-backups",
    private val httpClient: HttpClient = HttpClient.newBuilder()
        .connectTimeout(Duration.ofSeconds(5))
        .build(),
    private val serviceToken: String = "",
) : ArchiveStore {
    override fun put(projectId: UUID, backupId: UUID, bytes: ByteArray): String {
        val key = "$backupId.dump"
        val uri = URI.create(storageUrl.trimEnd('/') + "/v1/buckets/$bucket/objects/$key")
        val builder = HttpRequest.newBuilder(uri)
            .timeout(Duration.ofSeconds(60))
            .header("Content-Type", "application/octet-stream")
            .header("X-Forge-Project", projectId.toString())
            .header("X-Expected-SHA256", BackupChecksum.sha256Hex(bytes))
            .PUT(HttpRequest.BodyPublishers.ofByteArray(bytes))
        if (serviceToken.isNotBlank()) {
            builder.header("Authorization", "Bearer $serviceToken")
        }
        val response = try {
            httpClient.send(builder.build(), HttpResponse.BodyHandlers.ofString())
        } catch (e: Exception) {
            throw ArchiveStoreException("storage put failed: ${e.message}", e)
        }
        if (response.statusCode() !in setOf(200, 201)) {
            throw ArchiveStoreException(
                "storage put failed status=${response.statusCode()}: ${response.body()}",
            )
        }
        return "storage://$bucket/$key"
    }

    override fun get(projectId: UUID, location: String): ByteArray? {
        val key = storageKey(location) ?: return null
        val uri = URI.create(storageUrl.trimEnd('/') + "/v1/buckets/$bucket/objects/$key")
        val builder = HttpRequest.newBuilder(uri)
            .timeout(Duration.ofSeconds(60))
            .header("X-Forge-Project", projectId.toString())
            .GET()
        if (serviceToken.isNotBlank()) {
            builder.header("Authorization", "Bearer $serviceToken")
        }
        val response = try {
            httpClient.send(builder.build(), HttpResponse.BodyHandlers.ofByteArray())
        } catch (e: Exception) {
            throw ArchiveStoreException("storage get failed: ${e.message}", e)
        }
        return when (response.statusCode()) {
            200 -> response.body()
            404 -> null
            else -> throw ArchiveStoreException(
                "storage get failed status=${response.statusCode()}",
            )
        }
    }

    override fun delete(projectId: UUID, location: String) {
        val key = storageKey(location) ?: return
        val uri = URI.create(storageUrl.trimEnd('/') + "/v1/buckets/$bucket/objects/$key")
        val builder = HttpRequest.newBuilder(uri)
            .timeout(Duration.ofSeconds(30))
            .header("X-Forge-Project", projectId.toString())
            .DELETE()
        if (serviceToken.isNotBlank()) {
            builder.header("Authorization", "Bearer $serviceToken")
        }
        try {
            httpClient.send(builder.build(), HttpResponse.BodyHandlers.discarding())
        } catch (_: Exception) {
            // best effort
        }
    }

    private fun storageKey(location: String): String? {
        val prefix = "storage://$bucket/"
        if (!location.startsWith(prefix)) return null
        return location.removePrefix(prefix).takeIf { it.isNotBlank() }
    }
}

class ArchiveStoreException(message: String, cause: Throwable? = null) : RuntimeException(message, cause)

fun buildArchiveStore(
    target: String,
    backupDir: Path,
    storageUrl: String,
    bucket: String,
    serviceToken: String = "",
): ArchiveStore =
    when (target.lowercase()) {
        "storage" -> {
            if (storageUrl.isBlank()) {
                VolumeArchiveStore(backupDir)
            } else {
                StorageArchiveStore(storageUrl, bucket, serviceToken = serviceToken)
            }
        }
        else -> VolumeArchiveStore(backupDir)
    }

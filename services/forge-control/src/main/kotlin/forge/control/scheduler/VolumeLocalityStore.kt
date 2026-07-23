package forge.control.scheduler

import forge.control.repo.runSql
import forge.control.repo.withConnection
import java.util.concurrent.ConcurrentHashMap
import javax.sql.DataSource

/** Maps volume refs to the node that currently hosts the volume (scheduler contract). */
interface VolumeLocalityStore {
    fun get(volumeRef: String): String?

    fun put(volumeRef: String, nodeId: String)

    fun remove(volumeRef: String): Boolean
}

class InMemoryVolumeLocalityStore : VolumeLocalityStore {
    private val rows = ConcurrentHashMap<String, String>()

    override fun get(volumeRef: String): String? = rows[volumeRef]

    override fun put(volumeRef: String, nodeId: String) {
        rows[volumeRef] = nodeId
    }

    override fun remove(volumeRef: String): Boolean = rows.remove(volumeRef) != null
}

class JdbcVolumeLocalityStore(
    private val dataSource: DataSource,
) : VolumeLocalityStore {
    override fun get(volumeRef: String): String? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                "SELECT node_id FROM control.volume_locality WHERE volume_ref = ?",
            ).use { ps ->
                ps.setString(1, volumeRef)
                ps.executeQuery().use { rs ->
                    if (!rs.next()) null else rs.getString("node_id")
                }
            }
        }
    }

    override fun put(volumeRef: String, nodeId: String) = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                INSERT INTO control.volume_locality(volume_ref, node_id, updated_at)
                VALUES (?, ?, now())
                ON CONFLICT (volume_ref) DO UPDATE SET
                    node_id = EXCLUDED.node_id,
                    updated_at = now()
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, volumeRef)
                ps.setString(2, nodeId)
                ps.executeUpdate()
            }
            Unit
        }
    }

    override fun remove(volumeRef: String): Boolean = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                "DELETE FROM control.volume_locality WHERE volume_ref = ?",
            ).use { ps ->
                ps.setString(1, volumeRef)
                ps.executeUpdate() > 0
            }
        }
    }
}

package forge.control.scheduler

import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotEquals

class PlacementStoreTest {
    @Test
    fun upsertIsIdempotentForDeploymentAndReplicaIndex() {
        val store = InMemoryPlacementStore()
        val deploymentId = UUID.fromString("11111111-1111-1111-1111-111111111111")
        val first = Placement(
            id = "plc_first",
            deploymentId = deploymentId,
            replicaIndex = 0,
            nodeId = "node-local",
            strategy = "single-node",
            reason = "only node available",
            createdAt = Instant.parse("2026-07-22T12:00:00Z"),
        )
        val second = first.copy(
            id = "plc_second",
            nodeId = "node-other",
            reason = "should not overwrite",
        )

        val saved = store.upsert(first)
        val again = store.upsert(second)

        assertEquals("plc_first", saved.id)
        assertEquals("plc_first", again.id)
        assertEquals("node-local", again.nodeId)
        assertEquals(first, again)
        assertNotEquals(second.id, again.id)
        assertEquals(listOf(saved), store.listByDeployment(deploymentId))
    }
}

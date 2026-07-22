package forge.control.scheduler

import forge.control.scheduler.model.PlacementDecision
import forge.control.scheduler.model.PlacementRequest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertTrue

class SingleNodeSchedulerTest {
    @Test
    fun returnsSoleNodeWithSingleNodeStrategy() {
        val scheduler = SingleNodeScheduler("node-local")
        val decision = scheduler.place(
            PlacementRequest(deploymentId = "dpl-1", replicaIndex = 0),
        )
        val assigned = assertIs<PlacementDecision.Assigned>(decision)
        assertEquals("node-local", assigned.nodeId)
        assertEquals(SingleNodeScheduler.STRATEGY, assigned.strategy)
        assertEquals("single-node", assigned.strategy)
        assertTrue(assigned.reason.contains("only node"))
    }

    @Test
    fun noNodeReturnsTypedNoNodeAvailable() {
        val scheduler = SingleNodeScheduler(nodeId = null)
        val decision = scheduler.place(
            PlacementRequest(deploymentId = "dpl-1", replicaIndex = 0),
        )
        val missing = assertIs<PlacementDecision.NoNodeAvailable>(decision)
        assertEquals("no node available", missing.reason)
    }

    @Test
    fun blankNodeIdIsTreatedAsNoNode() {
        val scheduler = SingleNodeScheduler("   ")
        val decision = scheduler.place(
            PlacementRequest(deploymentId = "dpl-1", replicaIndex = 1),
        )
        assertIs<PlacementDecision.NoNodeAvailable>(decision)
    }
}

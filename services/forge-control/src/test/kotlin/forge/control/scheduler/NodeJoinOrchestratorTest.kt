package forge.control.scheduler

import forge.control.http.ApiException
import forge.control.logging.JsonLog
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull
import kotlin.test.assertTrue

class NodeJoinOrchestratorTest {
    private val log = JsonLog("forge-control-test", "error")

    @Test
    fun sequencesRegisterLeaseResponse() {
        val nodes = InMemoryNodeStore()
        val tokens = InMemoryBootstrapTokenStore()
        val t0 = Instant.parse("2026-07-23T10:00:00Z")
        val issued = tokens.issue("forge-labs", null, 900, t0)
        val network = object : NetworkClient {
            override fun allocateNodeLease(networkName: String, nodeId: String): NetworkLeaseResult =
                NetworkLeaseResult.Ok(
                    NodeNetworkLease(nodeId, "10.100.1.0/24", "10.100.1.1"),
                )
        }
        val orch = NodeJoinOrchestrator(
            nodes = nodes,
            tokens = tokens,
            network = network,
            log = log,
            clock = { t0 },
        )

        val result = orch.register(
            JoinRegisterCommand(
                nodeId = "node-a",
                address = "http://runtime-a:4102",
                capacity = NodeCapacity(slots = 4),
                bootstrapToken = issued.plaintext,
                wireguardPublicKey = "b64:testkey",
            ),
        )
        assertEquals("joining", result.node.status)
        assertEquals("10.100.1.0/24", result.node.networkCidr)
        assertEquals("10.100.1.1", result.node.networkGateway)
        assertTrue(result.created)
        assertTrue(result.peers.isEmpty())
    }

    @Test
    fun forgeNetworkFailureLeavesPendingNetworkNotOffline() {
        val nodes = InMemoryNodeStore()
        val tokens = InMemoryBootstrapTokenStore()
        val t0 = Instant.parse("2026-07-23T10:00:00Z")
        val issued = tokens.issue("forge-labs", null, 900, t0)
        val network = object : NetworkClient {
            override fun allocateNodeLease(networkName: String, nodeId: String): NetworkLeaseResult =
                NetworkLeaseResult.Unavailable("connection refused")
        }
        val orch = NodeJoinOrchestrator(
            nodes = nodes,
            tokens = tokens,
            network = network,
            log = log,
            clock = { t0 },
        )

        val result = orch.register(
            JoinRegisterCommand(
                nodeId = "node-a",
                address = "http://runtime-a:4102",
                capacity = NodeCapacity(slots = 4),
                bootstrapToken = issued.plaintext,
                wireguardPublicKey = "b64:testkey",
            ),
        )
        assertEquals("pending-network", result.node.status)
        assertNull(result.node.networkCidr)
        assertEquals("pending-network", nodes.find("node-a")!!.status)
    }

    @Test
    fun missingTokenRejectedWhenNetworkConfigured() {
        val nodes = InMemoryNodeStore()
        val tokens = InMemoryBootstrapTokenStore()
        val network = object : NetworkClient {
            override fun allocateNodeLease(networkName: String, nodeId: String): NetworkLeaseResult =
                NetworkLeaseResult.Ok(NodeNetworkLease("x", "10.100.1.0/24", "10.100.1.1"))
        }
        val orch = NodeJoinOrchestrator(
            nodes = nodes,
            tokens = tokens,
            network = network,
            log = log,
            requireTokenWhenNetworkConfigured = true,
        )
        val err = assertFailsWith<ApiException.Unauthorized> {
            orch.register(
                JoinRegisterCommand(
                    nodeId = "node-a",
                    address = "http://runtime-a:4102",
                    capacity = NodeCapacity(slots = 4),
                    bootstrapToken = null,
                    wireguardPublicKey = null,
                ),
            )
        }
        assertEquals("InvalidBootstrapToken", err.code)
    }

    @Test
    fun legacyPathWithoutNetworkStillGoesOnline() {
        val nodes = InMemoryNodeStore()
        val tokens = InMemoryBootstrapTokenStore()
        val orch = NodeJoinOrchestrator(
            nodes = nodes,
            tokens = tokens,
            network = null,
            log = log,
            requireTokenWhenNetworkConfigured = false,
        )
        val result = orch.register(
            JoinRegisterCommand(
                nodeId = "node-a",
                address = "http://runtime-a:4102",
                capacity = NodeCapacity(slots = 4),
                bootstrapToken = null,
                wireguardPublicKey = null,
            ),
        )
        assertEquals("online", result.node.status)
    }
}

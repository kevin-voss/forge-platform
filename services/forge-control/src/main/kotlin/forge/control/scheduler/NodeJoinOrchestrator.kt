package forge.control.scheduler

import forge.control.http.ApiException
import forge.control.logging.JsonLog
import forge.control.telemetry.Telemetry
import java.time.Instant

data class JoinRegisterCommand(
    val nodeId: String,
    val address: String,
    val capacity: NodeCapacity,
    val bootstrapToken: String?,
    val wireguardPublicKey: String?,
    val facts: NodeSchedulingFacts = NodeSchedulingFacts(),
)

data class JoinRegisterResult(
    val node: FleetNode,
    val created: Boolean,
    val peers: List<PeerInfo> = emptyList(),
)

data class PeerInfo(
    val nodeId: String,
    val publicKey: String,
    val endpoint: String? = null,
    val allowedIps: List<String> = emptyList(),
)

/**
 * Sequences register → verify bootstrap token → forge-network lease → respond.
 * Legacy docker-mode registration (no token, network client unset) stays on the
 * 08.02 path and lands `online` immediately.
 */
class NodeJoinOrchestrator(
    private val nodes: NodeStore,
    private val tokens: BootstrapTokenStore,
    private val network: NetworkClient?,
    private val networkName: String = "cluster-overlay",
    private val certificates: NodeCertificateIssuer = NoOpNodeCertificateIssuer,
    private val log: JsonLog,
    private val telemetry: Telemetry = Telemetry.current(),
    private val clock: () -> Instant = { Instant.now() },
    private val requireTokenWhenNetworkConfigured: Boolean = true,
) {
    fun register(cmd: JoinRegisterCommand): JoinRegisterResult {
        val started = System.nanoTime()
        val span = telemetry.startSpan("node.join")
        span.setAttribute("node.id", cmd.nodeId)
        return try {
            val result = doRegister(cmd)
            telemetry.recordNodeJoin("success", durationSeconds(started))
            result
        } catch (e: ApiException) {
            val result = when (e.code) {
                "InvalidBootstrapToken" -> {
                    val reason = e.details?.get("reason").orEmpty()
                    when {
                        reason == "expired" -> "expired_token"
                        else -> "invalid_token"
                    }
                }
                else -> null
            }
            if (result != null) {
                telemetry.recordNodeJoin(result, durationSeconds(started))
            }
            throw e
        } catch (e: Exception) {
            span.recordException(e)
            throw e
        } finally {
            span.end()
        }
    }

    private fun doRegister(cmd: JoinRegisterCommand): JoinRegisterResult {
        val at = clock()
        val nodeId = cmd.nodeId.trim()
        val address = cmd.address.trim()
        val existing = nodes.find(nodeId)
        val token = cmd.bootstrapToken?.trim()?.takeIf { it.isNotEmpty() }
        val publicKey = cmd.wireguardPublicKey?.trim()?.takeIf { it.isNotEmpty() }

        // Resume / idempotent re-register: joined, unrevoked identity needs no new token.
        if (existing != null && !existing.keyRevoked && existing.wireguardPublicKey != null && token == null) {
            val refreshed = nodes.registerJoin(
                id = nodeId,
                address = address,
                capacity = cmd.capacity,
                status = existing.status,
                wireguardPublicKey = existing.wireguardPublicKey,
                networkCidr = existing.networkCidr,
                networkGateway = existing.networkGateway,
                joinedAt = existing.joinedAt,
                at = at,
                clearKeyRevocation = false,
                facts = cmd.facts,
            )
            log.info(
                "node join resume (no token)",
                "node_id" to nodeId,
                "from_status" to existing.status,
                "to_status" to refreshed.status,
            )
            return JoinRegisterResult(node = refreshed, created = false)
        }

        if (token == null) {
            val networkConfigured = network != null && network !is NoOpNetworkClient
            if (networkConfigured && requireTokenWhenNetworkConfigured) {
                throw JdbcBootstrapTokenStore.invalidBootstrapToken("missing")
            }
            // Legacy 08.02 path (docker / single Compose network).
            val created = existing == null
            val node = nodes.register(nodeId, address, cmd.capacity, at, facts = cmd.facts)
            return JoinRegisterResult(node = node, created = created)
        }

        if (publicKey == null) {
            throw ApiException.BadRequest(
                "wireguard_public_key is required with bootstrap_token",
                mapOf("field" to "wireguard_public_key"),
            )
        }

        tokens.consume(token, nodeId, at)

        val created = existing == null || existing.keyRevoked
        val pending = nodes.registerJoin(
            id = nodeId,
            address = address,
            capacity = cmd.capacity,
            status = "pending-network",
            wireguardPublicKey = publicKey,
            networkCidr = null,
            networkGateway = null,
            joinedAt = null,
            at = at,
            clearKeyRevocation = true,
            facts = cmd.facts,
        )
        logJoinTransition(nodeId, existing?.status, "pending-network")
        telemetry.recordNodeStatus("pending-network")

        // Certificate seam (noop until epic 34).
        certificates.issueNodeCertificate(nodeId, publicKey)

        val networkClient = network ?: NoOpNetworkClient
        when (val lease = networkClient.allocateNodeLease(networkName, nodeId)) {
            is NetworkLeaseResult.Ok -> {
                val joining = nodes.registerJoin(
                    id = nodeId,
                    address = address,
                    capacity = cmd.capacity,
                    status = "joining",
                    wireguardPublicKey = publicKey,
                    networkCidr = lease.lease.cidr,
                    networkGateway = lease.lease.gateway,
                    joinedAt = at,
                    at = at,
                    clearKeyRevocation = true,
                    facts = cmd.facts,
                )
                logJoinTransition(nodeId, "pending-network", "joining")
                telemetry.recordNodeStatus("joining")
                return JoinRegisterResult(node = joining, created = created, peers = emptyList())
            }
            is NetworkLeaseResult.NoAddress -> {
                telemetry.recordNodeJoin("no_address", 0.0)
                log.warn(
                    "node join: no address from forge-network",
                    "node_id" to nodeId,
                    "detail" to lease.detail,
                )
                // Leave pending-network; surface conflict to caller.
                throw ApiException.Conflict(
                    "no address available for node join",
                    details = mapOf("node_id" to nodeId, "reason" to "no_address"),
                    code = "NoAddressSpaceAvailable",
                )
            }
            is NetworkLeaseResult.Unavailable, is NetworkLeaseResult.Failed -> {
                val detail = when (lease) {
                    is NetworkLeaseResult.Unavailable -> lease.detail
                    is NetworkLeaseResult.Failed -> lease.detail
                    else -> "unknown"
                }
                log.warn(
                    "node join: forge-network lease failed; node stays pending-network",
                    "node_id" to nodeId,
                    "detail" to detail,
                )
                // Node remains pending-network (not offline); caller may retry.
                return JoinRegisterResult(node = pending, created = created, peers = emptyList())
            }
        }
    }

    fun revokeKey(nodeId: String, at: Instant = clock()): FleetNode? {
        val existing = nodes.find(nodeId) ?: return null
        val revoked = nodes.revokeKey(nodeId, at) ?: return null
        log.info(
            "node key revoked",
            "node_id" to nodeId,
            "from_status" to existing.status,
            "to_status" to revoked.status,
        )
        return revoked
    }

    private fun logJoinTransition(nodeId: String, from: String?, to: String) {
        log.info(
            "node join status transition",
            "node_id" to nodeId,
            "from_status" to (from ?: "none"),
            "to_status" to to,
        )
    }

    private fun durationSeconds(startedNanos: Long): Double =
        (System.nanoTime() - startedNanos) / 1_000_000_000.0
}

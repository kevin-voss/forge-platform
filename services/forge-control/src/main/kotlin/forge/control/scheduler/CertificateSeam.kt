package forge.control.scheduler

/**
 * Seam for epic 34 (internal CA): issue a node certificate bound to [nodeId] + [publicKey].
 * Stubbed in 22.02 — no real issuance yet.
 */
fun interface NodeCertificateIssuer {
    fun issueNodeCertificate(nodeId: String, publicKey: String): NodeCertificate
}

data class NodeCertificate(
    val nodeId: String,
    val publicKey: String,
    /** PEM or opaque cert material; null while the seam is a no-op. */
    val certificatePem: String? = null,
    val stub: Boolean = true,
)

/** No-op issuer until epic 34 wires a real CA. */
object NoOpNodeCertificateIssuer : NodeCertificateIssuer {
    override fun issueNodeCertificate(nodeId: String, publicKey: String): NodeCertificate =
        NodeCertificate(nodeId = nodeId, publicKey = publicKey, certificatePem = null, stub = true)
}

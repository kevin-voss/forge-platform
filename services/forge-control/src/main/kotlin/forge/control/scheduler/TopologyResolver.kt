package forge.control.scheduler

/**
 * Resolves a node's value for a topology key (`node`, `zone`, `region`, `provider`).
 */
object TopologyResolver {
    const val KEY_NODE: String = "node"
    const val KEY_ZONE: String = "zone"
    const val KEY_REGION: String = "region"
    const val KEY_PROVIDER: String = "provider"

    val KEYS: Set<String> = setOf(KEY_NODE, KEY_ZONE, KEY_REGION, KEY_PROVIDER)

    fun resolve(node: FleetNode, topologyKey: String): String {
        return when (topologyKey.trim().lowercase()) {
            KEY_NODE -> node.id
            KEY_ZONE -> node.zone.ifBlank { "default" }
            KEY_REGION -> node.region.ifBlank { "default" }
            KEY_PROVIDER -> node.provider.ifBlank { "docker" }
            else -> throw IllegalArgumentException(
                "topologyKey must be node|zone|region|provider, got '$topologyKey'",
            )
        }
    }

    fun parseKey(raw: String?): String {
        val key = raw?.trim()?.lowercase().orEmpty()
        if (key !in KEYS) {
            throw IllegalArgumentException(
                "topologyKey must be node|zone|region|provider, got '$raw'",
            )
        }
        return key
    }
}

package forge.control.auth

import java.util.concurrent.ConcurrentHashMap

/**
 * Short-TTL in-memory cache for Identity introspection results (09.06).
 * Keys are opaque tokens; values expire so revocation is honored quickly.
 */
class IntrospectionCache(
    private val ttlMillis: Long,
    private val clock: () -> Long = { System.currentTimeMillis() },
) {
    private data class Entry(
        val value: IntrospectResult,
        val expiresAtMs: Long,
    )

    private val entries = ConcurrentHashMap<String, Entry>()

    fun get(token: String): IntrospectResult? {
        val now = clock()
        val entry = entries[token] ?: return null
        if (entry.expiresAtMs <= now) {
            entries.remove(token, entry)
            return null
        }
        return entry.value
    }

    fun put(token: String, value: IntrospectResult) {
        entries[token] = Entry(value, clock() + ttlMillis)
    }

    fun invalidate(token: String) {
        entries.remove(token)
    }

    fun clear() {
        entries.clear()
    }
}

/**
 * Short-TTL cache for Identity authz/check decisions.
 */
class AuthzCache(
    private val ttlMillis: Long,
    private val clock: () -> Long = { System.currentTimeMillis() },
) {
    private data class Entry(
        val value: AuthzDecision,
        val expiresAtMs: Long,
    )

    private val entries = ConcurrentHashMap<String, Entry>()

    fun get(key: String): AuthzDecision? {
        val now = clock()
        val entry = entries[key] ?: return null
        if (entry.expiresAtMs <= now) {
            entries.remove(key, entry)
            return null
        }
        return entry.value
    }

    fun put(key: String, value: AuthzDecision) {
        entries[key] = Entry(value, clock() + ttlMillis)
    }

    fun clear() {
        entries.clear()
    }

    companion object {
        fun key(principalType: String, principalId: String, projectId: String, action: String): String =
            "$principalType|$principalId|$projectId|$action"
    }
}

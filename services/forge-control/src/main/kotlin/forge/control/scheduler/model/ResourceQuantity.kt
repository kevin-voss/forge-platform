package forge.control.scheduler.model

/**
 * Parse/format Kubernetes-style CPU and memory quantity strings.
 * CPU: `"1000m"` (millicores) or bare cores (`"1"` → 1000m).
 * Memory: `"1024Mi"` / `"1Gi"` → megabytes (binary base).
 */
object ResourceQuantity {
    fun parseCpuMillis(raw: String): Int {
        val s = raw.trim()
        require(s.isNotEmpty()) { "cpu quantity must not be blank" }
        return when {
            s.endsWith("m", ignoreCase = true) -> {
                val n = s.dropLast(1).trim().toIntOrNull()
                    ?: throw IllegalArgumentException("invalid cpu millicores '$raw'")
                require(n >= 0) { "cpu millicores must be >= 0" }
                n
            }
            else -> {
                val cores = s.toDoubleOrNull()
                    ?: throw IllegalArgumentException("invalid cpu quantity '$raw'")
                require(cores >= 0) { "cpu must be >= 0" }
                (cores * 1000.0).toInt()
            }
        }
    }

    fun formatCpuMillis(millis: Int): String =
        if (millis % 1000 == 0) "${millis / 1000}" else "${millis}m"

    fun parseMemoryMb(raw: String): Int {
        val s = raw.trim()
        require(s.isNotEmpty()) { "memory quantity must not be blank" }
        val lower = s.lowercase()
        return when {
            lower.endsWith("gi") -> {
                val n = lower.dropLast(2).trim().toDoubleOrNull()
                    ?: throw IllegalArgumentException("invalid memory quantity '$raw'")
                require(n >= 0) { "memory must be >= 0" }
                (n * 1024.0).toInt()
            }
            lower.endsWith("mi") -> {
                val n = lower.dropLast(2).trim().toDoubleOrNull()
                    ?: throw IllegalArgumentException("invalid memory quantity '$raw'")
                require(n >= 0) { "memory must be >= 0" }
                n.toInt()
            }
            lower.endsWith("ki") -> {
                val n = lower.dropLast(2).trim().toDoubleOrNull()
                    ?: throw IllegalArgumentException("invalid memory quantity '$raw'")
                require(n >= 0) { "memory must be >= 0" }
                (n / 1024.0).toInt().coerceAtLeast(0)
            }
            lower.endsWith("g") -> {
                val n = lower.dropLast(1).trim().toDoubleOrNull()
                    ?: throw IllegalArgumentException("invalid memory quantity '$raw'")
                require(n >= 0) { "memory must be >= 0" }
                (n * 1000.0).toInt()
            }
            lower.endsWith("m") -> {
                val n = lower.dropLast(1).trim().toDoubleOrNull()
                    ?: throw IllegalArgumentException("invalid memory quantity '$raw'")
                require(n >= 0) { "memory must be >= 0" }
                n.toInt()
            }
            else -> {
                val bytes = s.toLongOrNull()
                    ?: throw IllegalArgumentException("invalid memory quantity '$raw'")
                require(bytes >= 0) { "memory must be >= 0" }
                (bytes / (1024L * 1024L)).toInt()
            }
        }
    }

    fun formatMemoryMb(mb: Int): String = "${mb}Mi"
}

/** Numeric resource bundle used in requests/limits and capacity accounting. */
@kotlinx.serialization.Serializable
data class ResourceBundle(
    @kotlinx.serialization.SerialName("cpu_millis") val cpuMillis: Int? = null,
    @kotlinx.serialization.SerialName("memory_mb") val memoryMb: Int? = null,
    @kotlinx.serialization.SerialName("disk_mb") val diskMb: Int? = null,
) {
    fun isEmpty(): Boolean = cpuMillis == null && memoryMb == null && diskMb == null
}

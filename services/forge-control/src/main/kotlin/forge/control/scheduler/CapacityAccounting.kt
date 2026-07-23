package forge.control.scheduler

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class NodeReserved(
    @SerialName("cpu_millis") val cpuMillis: Int = 0,
    @SerialName("mem_mb") val memMb: Int = 0,
    @SerialName("disk_mb") val diskMb: Int = 0,
)

data class OvercommitConfig(
    val cpuRatio: Double = 1.0,
    val memoryRatio: Double = 1.0,
    val systemReservedCpuMillis: Int = 100,
    val systemReservedMemoryMb: Int = 256,
    val systemReservedDiskMb: Int = 0,
) {
    init {
        require(cpuRatio > 0.0) { "cpuRatio must be > 0" }
        require(memoryRatio > 0.0) { "memoryRatio must be > 0" }
        require(systemReservedCpuMillis >= 0) { "systemReservedCpuMillis must be >= 0" }
        require(systemReservedMemoryMb >= 0) { "systemReservedMemoryMb must be >= 0" }
        require(systemReservedDiskMb >= 0) { "systemReservedDiskMb must be >= 0" }
    }
}

/**
 * Compute allocatable capacity: capacity × overcommit − system-reserved headroom.
 * Disk is never overcommitted.
 */
object CapacityAccounting {
    fun allocatable(
        capacity: NodeCapacity,
        reserved: NodeReserved = NodeReserved(),
        config: OvercommitConfig = OvercommitConfig(),
    ): NodeCapacity {
        val cpu = capacity.cpuMillis?.let { total ->
            val overcommitted = (total.toDouble() * config.cpuRatio).toInt()
            (overcommitted - config.systemReservedCpuMillis - reserved.cpuMillis).coerceAtLeast(0)
        }
        val mem = capacity.memMb?.let { total ->
            val overcommitted = (total.toDouble() * config.memoryRatio).toInt()
            (overcommitted - config.systemReservedMemoryMb - reserved.memMb).coerceAtLeast(0)
        }
        val disk = capacity.diskMb?.let { total ->
            (total - config.systemReservedDiskMb - reserved.diskMb).coerceAtLeast(0)
        }
        return NodeCapacity(
            slots = capacity.slots,
            cpuMillis = cpu,
            memMb = mem,
            diskMb = disk,
            // GPU is never overcommitted; surface capacity as-is for allocatable.
            gpu = capacity.gpu,
        )
    }

    /** Merge platform defaults with any node-reported reserved headroom. */
    fun effectiveReserved(
        nodeReserved: NodeReserved?,
        config: OvercommitConfig,
    ): NodeReserved =
        NodeReserved(
            cpuMillis = (nodeReserved?.cpuMillis ?: 0).coerceAtLeast(0),
            memMb = (nodeReserved?.memMb ?: 0).coerceAtLeast(0),
            diskMb = (nodeReserved?.diskMb ?: 0).coerceAtLeast(0),
        ).let {
            // Platform system-reserved is applied inside allocatable(); node reserved is additive.
            it
        }
}

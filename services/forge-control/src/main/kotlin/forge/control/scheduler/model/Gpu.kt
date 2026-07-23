package forge.control.scheduler.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * GPU request on a placement (vendor/model exact match; memory is a lower bound).
 */
@Serializable
data class GpuRequest(
    val count: Int = 1,
    val vendor: String? = null,
    val model: String? = null,
    /** Memory in MiB; callers may also supply [memory] as a quantity string. */
    @SerialName("memory_mb") val memoryMb: Int? = null,
    val memory: String? = null,
    val driver: String? = null,
) {
    init {
        require(count >= 1) { "gpu.count must be >= 1" }
        memoryMb?.let { require(it >= 0) { "gpu.memory_mb must be >= 0" } }
    }

    fun resolvedMemoryMb(): Int? =
        memoryMb ?: memory?.takeIf { it.isNotBlank() }?.let { ResourceQuantity.parseMemoryMb(it) }

    fun isEmpty(): Boolean =
        count <= 0 && vendor.isNullOrBlank() && model.isNullOrBlank() &&
            memoryMb == null && memory.isNullOrBlank() && driver.isNullOrBlank()
}

/**
 * Node-reported GPU capacity (allocatable when present on [forge.control.scheduler.NodeCapacity]).
 */
@Serializable
data class GpuCapacity(
    val count: Int = 0,
    val vendor: String? = null,
    val model: String? = null,
    @SerialName("memory_mb") val memoryMb: Int? = null,
    val memory: String? = null,
    val driver: String? = null,
) {
    init {
        require(count >= 0) { "gpu.count must be >= 0" }
    }

    fun resolvedMemoryMb(): Int? =
        memoryMb ?: memory?.takeIf { it.isNotBlank() }?.let { ResourceQuantity.parseMemoryMb(it) }

    fun isEmpty(): Boolean = count <= 0
}

/** Match [request] against node [capacity], ignoring free-count (caller checks allocation). */
object GpuMatcher {
    fun matches(capacity: GpuCapacity?, request: GpuRequest?): Boolean {
        if (request == null || request.isEmpty()) return true
        if (capacity == null || capacity.isEmpty()) return false
        if (capacity.count < request.count) return false
        if (!request.vendor.isNullOrBlank() &&
            !request.vendor.equals(capacity.vendor, ignoreCase = true)
        ) {
            return false
        }
        if (!request.model.isNullOrBlank() &&
            !request.model.equals(capacity.model, ignoreCase = true)
        ) {
            return false
        }
        val needMem = request.resolvedMemoryMb()
        if (needMem != null) {
            val have = capacity.resolvedMemoryMb() ?: return false
            if (have < needMem) return false
        }
        if (!request.driver.isNullOrBlank()) {
            val haveDriver = capacity.driver ?: return false
            if (!haveDriver.contains(request.driver, ignoreCase = true) &&
                !haveDriver.equals(request.driver, ignoreCase = true)
            ) {
                return false
            }
        }
        return true
    }
}

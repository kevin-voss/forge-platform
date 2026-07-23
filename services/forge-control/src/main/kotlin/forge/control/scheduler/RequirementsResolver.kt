package forge.control.scheduler

import forge.control.scheduler.model.ResourceBundle
import forge.control.scheduler.model.ResourceRequirements

data class SlotConversionConfig(
    val slotCpuMillis: Int = 1000,
    val slotMemoryMb: Int = 1024,
) {
    init {
        require(slotCpuMillis >= 1) { "slotCpuMillis must be >= 1" }
        require(slotMemoryMb >= 1) { "slotMemoryMb must be >= 1" }
    }
}

/**
 * Single authoritative source for a placement's resource requirements.
 * Exactly one of slots-derived or explicit requests is authoritative; never summed.
 */
data class ResolvedRequirements(
    val slots: Int,
    val requests: ResourceBundle,
    val limits: ResourceBundle?,
    /** True when the caller supplied `requests`; slots are informational only. */
    val requestsAuthoritative: Boolean,
) {
    val cpuMillis: Int? get() = requests.cpuMillis
    val memoryMb: Int? get() = requests.memoryMb
    val diskMb: Int? get() = requests.diskMb

    fun toResourceRequirements(): ResourceRequirements =
        if (requestsAuthoritative) {
            ResourceRequirements(
                slots = slots,
                cpuMillis = cpuMillis,
                memMb = memoryMb,
                diskMb = diskMb,
                requests = requests.takeUnless { it.isEmpty() },
                limits = limits,
                slotsExplicit = true,
                requestsAuthoritative = true,
            )
        } else {
            // Slots-authoritative: do not embed derived requests, or re-resolve
            // would treat them as explicit and break epic-08 slot-only fleets.
            ResourceRequirements(
                slots = slots,
                limits = limits,
                slotsExplicit = true,
                requestsAuthoritative = false,
            )
        }
}

object RequirementsResolver {
    @Volatile
    var defaultConfig: SlotConversionConfig = SlotConversionConfig()

    fun resolve(
        requirements: ResourceRequirements,
        config: SlotConversionConfig = defaultConfig,
    ): ResolvedRequirements {
        val limits = requirements.limits
        // Honor an already-resolved slots-authoritative marker.
        if (requirements.requestsAuthoritative == false) {
            val slots = requirements.slots.coerceAtLeast(1)
            val derived = ResourceBundle(
                cpuMillis = slots * config.slotCpuMillis,
                memoryMb = slots * config.slotMemoryMb,
                diskMb = null,
            )
            validateLimits(derived, limits)
            return ResolvedRequirements(
                slots = slots,
                requests = derived,
                limits = limits,
                requestsAuthoritative = false,
            )
        }
        val explicit = requirements.requests
        if (explicit != null && !explicit.isEmpty()) {
            validateLimits(explicit, limits)
            val slots = when {
                requirements.slots > 1 || requirements.slotsExplicit -> requirements.slots.coerceAtLeast(1)
                else -> slotsFromRequests(explicit, config)
            }
            return ResolvedRequirements(
                slots = slots,
                requests = explicit,
                limits = limits,
                requestsAuthoritative = true,
            )
        }

        // Legacy flat fields (cpuMillis/memMb) treated as explicit requests when set.
        if (requirements.cpuMillis != null || requirements.memMb != null || requirements.diskMb != null) {
            val bundle = ResourceBundle(
                cpuMillis = requirements.cpuMillis,
                memoryMb = requirements.memMb,
                diskMb = requirements.diskMb,
            )
            validateLimits(bundle, limits)
            return ResolvedRequirements(
                slots = requirements.slots.coerceAtLeast(1),
                requests = bundle,
                limits = limits,
                requestsAuthoritative = true,
            )
        }

        val slots = requirements.slots.coerceAtLeast(1)
        val derived = ResourceBundle(
            cpuMillis = slots * config.slotCpuMillis,
            memoryMb = slots * config.slotMemoryMb,
            diskMb = null,
        )
        validateLimits(derived, limits)
        return ResolvedRequirements(
            slots = slots,
            requests = derived,
            limits = limits,
            requestsAuthoritative = false,
        )
    }

    fun slotsFromRequests(requests: ResourceBundle, config: SlotConversionConfig): Int {
        val fromCpu = requests.cpuMillis?.let { ceilDiv(it, config.slotCpuMillis) } ?: 0
        val fromMem = requests.memoryMb?.let { ceilDiv(it, config.slotMemoryMb) } ?: 0
        return maxOf(1, fromCpu, fromMem)
    }

    fun validateLimits(requests: ResourceBundle, limits: ResourceBundle?) {
        if (limits == null || limits.isEmpty()) return
        val reqCpu = requests.cpuMillis
        val limCpu = limits.cpuMillis
        if (reqCpu != null && limCpu != null && limCpu < reqCpu) {
            throw LimitsNarrowerThanRequestsException(
                "limits.cpu_millis ($limCpu) must be >= requests.cpu_millis ($reqCpu)",
            )
        }
        val reqMem = requests.memoryMb
        val limMem = limits.memoryMb
        if (reqMem != null && limMem != null && limMem < reqMem) {
            throw LimitsNarrowerThanRequestsException(
                "limits.memory_mb ($limMem) must be >= requests.memory_mb ($reqMem)",
            )
        }
        val reqDisk = requests.diskMb
        val limDisk = limits.diskMb
        if (reqDisk != null && limDisk != null && limDisk < reqDisk) {
            throw LimitsNarrowerThanRequestsException(
                "limits.disk_mb ($limDisk) must be >= requests.disk_mb ($reqDisk)",
            )
        }
    }

    private fun ceilDiv(num: Int, den: Int): Int =
        if (num <= 0) 0 else (num + den - 1) / den
}

class LimitsNarrowerThanRequestsException(message: String) : IllegalArgumentException(message)

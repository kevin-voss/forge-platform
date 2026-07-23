package forge.control.scheduler

import forge.control.scheduler.model.ResourceBundle
import forge.control.scheduler.model.ResourceQuantity
import forge.control.scheduler.model.ResourceRequirements
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class RequirementsResolverTest {
    private val cfg = SlotConversionConfig(slotCpuMillis = 1000, slotMemoryMb = 1024)

    @Test
    fun slotsOnlyDerivesCpuAndMemory() {
        val resolved = RequirementsResolver.resolve(ResourceRequirements(slots = 2), cfg)
        assertFalse(resolved.requestsAuthoritative)
        assertEquals(2, resolved.slots)
        assertEquals(2000, resolved.cpuMillis)
        assertEquals(2048, resolved.memoryMb)
    }

    @Test
    fun requestsPresentAreAuthoritativeAndSlotsDerived() {
        val resolved = RequirementsResolver.resolve(
            ResourceRequirements(
                slots = 1,
                requests = ResourceBundle(cpuMillis = 500, memoryMb = 512),
            ),
            cfg,
        )
        assertTrue(resolved.requestsAuthoritative)
        assertEquals(500, resolved.cpuMillis)
        assertEquals(512, resolved.memoryMb)
        assertEquals(1, resolved.slots)
    }

    @Test
    fun requestsPresentWithExplicitSlotsKeepsSlotsInformational() {
        val resolved = RequirementsResolver.resolve(
            ResourceRequirements(
                slots = 3,
                slotsExplicit = true,
                requests = ResourceBundle(cpuMillis = 250, memoryMb = 256),
            ),
            cfg,
        )
        assertTrue(resolved.requestsAuthoritative)
        assertEquals(3, resolved.slots)
        assertEquals(250, resolved.cpuMillis)
    }

    @Test
    fun limitsNarrowerThanRequestsRejected() {
        assertFailsWith<LimitsNarrowerThanRequestsException> {
            RequirementsResolver.resolve(
                ResourceRequirements(
                    requests = ResourceBundle(cpuMillis = 1000, memoryMb = 512),
                    limits = ResourceBundle(cpuMillis = 500, memoryMb = 512),
                ),
                cfg,
            )
        }
    }

    @Test
    fun resourceQuantityParsesCpuAndMemoryStrings() {
        assertEquals(1000, ResourceQuantity.parseCpuMillis("1000m"))
        assertEquals(2000, ResourceQuantity.parseCpuMillis("2"))
        assertEquals(1024, ResourceQuantity.parseMemoryMb("1024Mi"))
        assertEquals(2048, ResourceQuantity.parseMemoryMb("2Gi"))
        assertEquals("512m", ResourceQuantity.formatCpuMillis(512))
        assertEquals("256Mi", ResourceQuantity.formatMemoryMb(256))
    }
}

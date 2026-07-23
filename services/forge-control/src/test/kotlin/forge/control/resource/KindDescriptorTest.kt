package forge.control.resource

import kotlin.test.Test
import kotlin.test.assertFailsWith
import kotlin.test.assertNull

class KindDescriptorTest {
    @Test
    fun clusterScopeRejectsParentKind() {
        assertFailsWith<IllegalArgumentException> {
            KindDescriptor(
                kind = "Region",
                plural = "regions",
                scope = ResourceScope.Cluster,
                parentKind = "Organization",
                schemaVersion = 1,
                owningController = "region-controller",
                idPrefix = "reg",
            )
        }
    }

    @Test
    fun clusterScopeAllowsNullParentKind() {
        val descriptor = KindDescriptor(
            kind = "Region",
            plural = "regions",
            scope = ResourceScope.Cluster,
            parentKind = null,
            schemaVersion = 1,
            owningController = "region-controller",
            idPrefix = "reg",
        )
        assertNull(descriptor.parentKind)
    }

    @Test
    fun environmentScopeAllowsParentKind() {
        KindDescriptor(
            kind = "Service",
            plural = "services",
            scope = ResourceScope.Environment,
            parentKind = "Application",
            schemaVersion = 1,
            owningController = "service-controller",
            idPrefix = "svc",
        )
    }
}

package forge.control.resource

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull
import kotlin.test.assertSame
import kotlin.test.assertTrue

class KindRegistryTest {
    private fun widget(): KindDescriptor =
        KindDescriptor(
            kind = "Widget",
            plural = "widgets",
            scope = ResourceScope.Environment,
            schemaVersion = 1,
            owningController = "widget-controller",
            idPrefix = "wgt",
        )

    @Test
    fun registerGetAndByPluralRoundTrip() {
        val registry = KindRegistry()
        val descriptor = widget()
        registry.register(descriptor)

        assertSame(descriptor, registry.get("Widget"))
        assertSame(descriptor, registry.byPlural("widgets"))
        assertEquals(listOf(descriptor), registry.all().toList())
    }

    @Test
    fun registeringSameKindTwiceThrows() {
        val registry = KindRegistry()
        registry.register(widget())
        assertFailsWith<IllegalArgumentException> {
            registry.register(widget())
        }
    }

    @Test
    fun registeringDuplicatePluralThrows() {
        val registry = KindRegistry()
        registry.register(widget())
        assertFailsWith<IllegalArgumentException> {
            registry.register(
                KindDescriptor(
                    kind = "Gadget",
                    plural = "widgets",
                    scope = ResourceScope.Environment,
                    schemaVersion = 1,
                    owningController = "gadget-controller",
                    idPrefix = "gdt",
                ),
            )
        }
        assertNull(registry.get("Gadget"))
        assertTrue(registry.get("Widget") != null)
    }
}

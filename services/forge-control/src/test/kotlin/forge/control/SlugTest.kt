package forge.control

import forge.control.service.Slug
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

class SlugTest {
    @Test
    fun deriveFromSimpleName() {
        assertEquals("acme", Slug.derive("Acme"))
        assertEquals("acme-corp", Slug.derive("Acme Corp"))
        assertEquals("hello-world", Slug.derive("  Hello   World  "))
    }

    @Test
    fun deriveStripsPunctuation() {
        assertEquals("foo-bar", Slug.derive("Foo_Bar!"))
        assertEquals("a-b-c", Slug.derive("a---b___c"))
    }

    @Test
    fun deriveEmptyWhenNoAlphanumerics() {
        assertEquals("", Slug.derive("!!!"))
        assertEquals("", Slug.derive("   "))
    }

    @Test
    fun deriveTruncatesToMaxLength() {
        val long = "a".repeat(Slug.MAX_SLUG_LENGTH + 20)
        assertEquals(Slug.MAX_SLUG_LENGTH, Slug.derive(long).length)
    }

    @Test
    fun validateAcceptsValidSlugs() {
        assertNull(Slug.validationError("acme"))
        assertNull(Slug.validationError("acme-corp"))
        assertNull(Slug.validationError("a1-b2"))
    }

    @Test
    fun validateRejectsInvalidSlugs() {
        assertTrue(Slug.validationError("")!!.isNotBlank())
        assertTrue(Slug.validationError("Acme")!!.isNotBlank())
        assertTrue(Slug.validationError("-acme")!!.isNotBlank())
        assertTrue(Slug.validationError("acme-")!!.isNotBlank())
        assertTrue(Slug.validationError("acme--corp")!!.isNotBlank())
        assertTrue(Slug.validationError("a".repeat(Slug.MAX_SLUG_LENGTH + 1))!!.isNotBlank())
    }

    @Test
    fun normalizeLowercasesAndTrims() {
        assertEquals("acme", Slug.normalize("  AcMe  "))
        assertNull(Slug.normalize("   "))
    }
}

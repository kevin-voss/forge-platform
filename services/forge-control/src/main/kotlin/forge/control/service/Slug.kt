package forge.control.service

/**
 * Project slug derivation and validation.
 * Slugs are lowercase `[a-z0-9]` segments separated by single hyphens.
 */
object Slug {
    const val MAX_NAME_LENGTH = 128
    const val MAX_SLUG_LENGTH = 64

    private val VALID = Regex("^[a-z0-9]+(?:-[a-z0-9]+)*$")

    /** Derive a slug from a display name; may return blank if name has no alphanumerics. */
    fun derive(name: String): String =
        name
            .trim()
            .lowercase()
            .replace(Regex("[^a-z0-9]+"), "-")
            .trim('-')
            .take(MAX_SLUG_LENGTH)
            .trimEnd('-')

    /**
     * Normalize an explicit slug (trim + lowercase). Returns the normalized value,
     * or null when the input is blank after trim.
     */
    fun normalize(raw: String): String? {
        val trimmed = raw.trim().lowercase()
        return trimmed.ifEmpty { null }
    }

    /** Returns an error message when invalid; null when valid. */
    fun validationError(slug: String): String? =
        when {
            slug.isBlank() -> "slug must not be blank"
            slug.length > MAX_SLUG_LENGTH -> "slug must be at most $MAX_SLUG_LENGTH characters"
            !VALID.matches(slug) ->
                "slug must be lowercase alphanumeric segments separated by single hyphens"
            else -> null
        }
}

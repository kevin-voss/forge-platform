package forge.control.resource

/**
 * Renders a [LabelSelector] predicate tree as a JSONB-aware SQL fragment with
 * JDBC bind parameters (never interpolates label values into SQL text).
 */
object LabelSelectorSql {
    data class Fragment(
        val sql: String,
        val params: List<Any?>,
    ) {
        companion object {
            val TRUE: Fragment = Fragment("TRUE", emptyList())
        }
    }

    fun render(selector: LabelSelector): Fragment {
        if (selector.terms.isEmpty()) return Fragment.TRUE
        val parts = mutableListOf<String>()
        val params = mutableListOf<Any?>()
        for (term in selector.terms) {
            val frag = renderTerm(term)
            parts += "(${frag.sql})"
            params.addAll(frag.params)
        }
        return Fragment(parts.joinToString(" AND "), params)
    }

    private fun renderTerm(term: LabelSelectorTerm): Fragment =
        when (term) {
            is LabelSelectorTerm.Equals ->
                // Prefer containment so GIN(labels) can be used.
                Fragment(
                    "labels @> jsonb_build_object(?::text, to_jsonb(?::text))",
                    listOf(term.key, term.value),
                )
            is LabelSelectorTerm.NotEquals ->
                Fragment(
                    "labels->>? IS DISTINCT FROM ?",
                    listOf(term.key, term.value),
                )
            is LabelSelectorTerm.In -> {
                val placeholders = term.values.joinToString(", ") { "?" }
                Fragment(
                    "labels->>? IN ($placeholders)",
                    listOf(term.key) + term.values,
                )
            }
            is LabelSelectorTerm.NotIn -> {
                val placeholders = term.values.joinToString(", ") { "?" }
                Fragment(
                    "(labels->>? IS NULL OR labels->>? NOT IN ($placeholders))",
                    listOf(term.key, term.key) + term.values,
                )
            }
            is LabelSelectorTerm.Exists ->
                // jsonb_exists avoids JDBC conflict with the JSONB `?` operator.
                Fragment("jsonb_exists(labels, ?)", listOf(term.key))
            is LabelSelectorTerm.DoesNotExist ->
                Fragment("NOT jsonb_exists(labels, ?)", listOf(term.key))
        }
}

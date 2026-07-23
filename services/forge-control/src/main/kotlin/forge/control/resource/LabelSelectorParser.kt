package forge.control.resource

import forge.control.http.ApiException

/**
 * Parses Kubernetes-style `labelSelector` query strings into a typed predicate tree.
 *
 * Grammar (terms AND-combined by comma):
 * ```
 * key=value | key!=value | key in (a,b) | key notin (a,b) | key | !key
 * ```
 */
object LabelSelectorParser {
    fun parse(raw: String?): LabelSelector {
        if (raw.isNullOrBlank()) return LabelSelector(emptyList())
        val terms = splitTerms(raw.trim())
        if (terms.isEmpty()) return LabelSelector(emptyList())
        return LabelSelector(terms.map { parseTerm(it) })
    }

    private fun splitTerms(input: String): List<String> {
        val out = mutableListOf<String>()
        val current = StringBuilder()
        var depth = 0
        for (ch in input) {
            when {
                ch == '(' -> {
                    depth++
                    current.append(ch)
                }
                ch == ')' -> {
                    depth--
                    if (depth < 0) {
                        throw malformed(input, input)
                    }
                    current.append(ch)
                }
                ch == ',' && depth == 0 -> {
                    val term = current.toString().trim()
                    if (term.isNotEmpty()) out += term
                    current.clear()
                }
                else -> current.append(ch)
            }
        }
        if (depth != 0) throw malformed(input, input)
        val last = current.toString().trim()
        if (last.isNotEmpty()) out += last
        return out
    }

    private fun parseTerm(term: String): LabelSelectorTerm {
        val t = term.trim()
        if (t.isEmpty()) throw malformed(term, term)

        if (t.startsWith("!")) {
            val key = t.substring(1).trim()
            if (!isKey(key)) throw malformed(term, term)
            return LabelSelectorTerm.DoesNotExist(key)
        }

        val notInIdx = indexOfKeyword(t, " notin ")
        if (notInIdx >= 0) {
            val key = t.substring(0, notInIdx).trim()
            val rest = t.substring(notInIdx + " notin ".length).trim()
            if (!isKey(key)) throw malformed(term, term)
            return LabelSelectorTerm.NotIn(key, parseSet(rest, term))
        }

        val inIdx = indexOfKeyword(t, " in ")
        if (inIdx >= 0) {
            val key = t.substring(0, inIdx).trim()
            val rest = t.substring(inIdx + " in ".length).trim()
            if (!isKey(key)) throw malformed(term, term)
            return LabelSelectorTerm.In(key, parseSet(rest, term))
        }

        val neIdx = t.indexOf("!=")
        if (neIdx >= 0) {
            val key = t.substring(0, neIdx).trim()
            val value = t.substring(neIdx + 2).trim()
            if (!isKey(key) || !isValue(value)) throw malformed(term, term)
            return LabelSelectorTerm.NotEquals(key, value)
        }

        val eqIdx = t.indexOf('=')
        if (eqIdx >= 0) {
            val key = t.substring(0, eqIdx).trim()
            val value = t.substring(eqIdx + 1).trim()
            if (!isKey(key) || !isValue(value)) throw malformed(term, term)
            return LabelSelectorTerm.Equals(key, value)
        }

        if (!isKey(t)) throw malformed(term, term)
        return LabelSelectorTerm.Exists(t)
    }

    private fun parseSet(raw: String, term: String): List<String> {
        if (!raw.startsWith("(") || !raw.endsWith(")")) {
            throw malformed(term, term)
        }
        val inner = raw.substring(1, raw.length - 1).trim()
        if (inner.isEmpty()) {
            throw malformed(term, term)
        }
        val values = inner.split(',').map { it.trim() }
        if (values.isEmpty() || values.any { it.isEmpty() || !isValue(it) }) {
            throw malformed(term, term)
        }
        return values
    }

    private fun indexOfKeyword(input: String, keyword: String): Int {
        var i = 0
        while (i <= input.length - keyword.length) {
            if (input.regionMatches(i, keyword, 0, keyword.length, ignoreCase = true)) {
                return i
            }
            i++
        }
        return -1
    }

    private fun isKey(key: String): Boolean {
        if (key.isEmpty()) return false
        return try {
            LabelValidator.validateKey(key)
            true
        } catch (_: ApiException) {
            false
        }
    }

    private fun isValue(value: String): Boolean {
        return try {
            LabelValidator.validateValue(value)
            true
        } catch (_: ApiException) {
            false
        }
    }

    private fun malformed(selector: String, term: String): Nothing =
        throw ApiException.BadRequest(
            "malformed labelSelector",
            details = mapOf("selector" to selector, "term" to term),
            code = "invalid_label_selector",
        )
}

data class LabelSelector(val terms: List<LabelSelectorTerm>)

sealed class LabelSelectorTerm {
    data class Equals(val key: String, val value: String) : LabelSelectorTerm()
    data class NotEquals(val key: String, val value: String) : LabelSelectorTerm()
    data class In(val key: String, val values: List<String>) : LabelSelectorTerm()
    data class NotIn(val key: String, val values: List<String>) : LabelSelectorTerm()
    data class Exists(val key: String) : LabelSelectorTerm()
    data class DoesNotExist(val key: String) : LabelSelectorTerm()
}

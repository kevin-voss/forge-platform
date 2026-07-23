package forge.control.resource

import forge.control.http.ApiException
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

/**
 * RFC 6902 JSON Patch restricted to writable resource paths:
 * `/spec…`, `/metadata/labels…`, `/metadata/annotations…`.
 */
object JsonPatch {
    private val allowedRoots = listOf(
        listOf("spec"),
        listOf("metadata", "labels"),
        listOf("metadata", "annotations"),
    )

    fun apply(document: JsonObject, operations: JsonArray): JsonObject {
        var current: JsonElement = document
        for (opElement in operations) {
            val op = opElement.jsonObject
            val opName = op["op"]?.jsonPrimitive?.contentOrNull
                ?: throw invalidPatch("patch operation missing op")
            val path = op["path"]?.jsonPrimitive?.contentOrNull
                ?: throw invalidPatch("patch operation missing path")
            val tokens = parsePath(path)
            ensureAllowed(tokens)
            current = when (opName) {
                "add" -> {
                    val value = op["value"] ?: throw invalidPatch("add requires value")
                    add(current, tokens, value)
                }
                "remove" -> remove(current, tokens)
                "replace" -> {
                    val value = op["value"] ?: throw invalidPatch("replace requires value")
                    replace(current, tokens, value)
                }
                "move" -> {
                    val from = op["from"]?.jsonPrimitive?.contentOrNull
                        ?: throw invalidPatch("move requires from")
                    val fromTokens = parsePath(from)
                    ensureAllowed(fromTokens)
                    val (without, moved) = removeReturning(current, fromTokens)
                    add(without, tokens, moved)
                }
                "copy" -> {
                    val from = op["from"]?.jsonPrimitive?.contentOrNull
                        ?: throw invalidPatch("copy requires from")
                    val fromTokens = parsePath(from)
                    ensureAllowed(fromTokens)
                    val copied = get(current, fromTokens)
                        ?: throw invalidPatch("copy from path not found: $from")
                    add(current, tokens, copied)
                }
                "test" -> {
                    val value = op["value"] ?: throw invalidPatch("test requires value")
                    val actual = get(current, tokens)
                    if (actual != value) {
                        throw invalidPatch("test failed at path $path")
                    }
                    current
                }
                else -> throw invalidPatch("unsupported patch op: $opName")
            }
        }
        return current.jsonObject
    }

    private fun ensureAllowed(tokens: List<String>) {
        if (tokens.isEmpty()) {
            throw ApiException.BadRequest(
                "patch path forbidden",
                details = mapOf("path" to "/"),
                code = "patch_path_forbidden",
            )
        }
        val allowed = allowedRoots.any { root ->
            tokens.size >= root.size && tokens.take(root.size) == root
        }
        if (!allowed) {
            throw ApiException.BadRequest(
                "patch path forbidden",
                details = mapOf("path" to encodePath(tokens)),
                code = "patch_path_forbidden",
            )
        }
    }

    private fun parsePath(path: String): List<String> {
        if (path.isEmpty() || path[0] != '/') {
            throw invalidPatch("invalid patch path: $path")
        }
        if (path == "/") return emptyList()
        return path.substring(1).split('/').map { unescape(it) }
    }

    private fun encodePath(tokens: List<String>): String =
        if (tokens.isEmpty()) "/" else tokens.joinToString("", prefix = "/") { escape(it) }

    private fun unescape(token: String): String =
        token.replace("~1", "/").replace("~0", "~")

    private fun escape(token: String): String =
        token.replace("~", "~0").replace("/", "~1")

    private fun get(element: JsonElement, tokens: List<String>): JsonElement? {
        if (tokens.isEmpty()) return element
        val head = tokens.first()
        val rest = tokens.drop(1)
        return when (element) {
            is JsonObject -> element[head]?.let { get(it, rest) }
            is JsonArray -> {
                val index = head.toIntOrNull() ?: return null
                if (index !in element.indices) null else get(element[index], rest)
            }
            else -> null
        }
    }

    private fun add(element: JsonElement, tokens: List<String>, value: JsonElement): JsonElement {
        if (tokens.isEmpty()) return value
        val head = tokens.first()
        val rest = tokens.drop(1)
        return when (element) {
            is JsonObject -> {
                if (rest.isEmpty()) {
                    buildJsonObject {
                        element.forEach { (k, v) -> put(k, v) }
                        put(head, value)
                    }
                } else {
                    val child = element[head] ?: when {
                        rest.first().toIntOrNull() != null -> JsonArray(emptyList())
                        else -> JsonObject(emptyMap())
                    }
                    buildJsonObject {
                        element.forEach { (k, v) -> if (k != head) put(k, v) }
                        put(head, add(child, rest, value))
                    }
                }
            }
            is JsonArray -> {
                val index = when (head) {
                    "-" -> element.size
                    else -> head.toIntOrNull()
                        ?: throw invalidPatch("invalid array index: $head")
                }
                if (rest.isEmpty()) {
                    if (index < 0 || index > element.size) {
                        throw invalidPatch("array index out of bounds: $index")
                    }
                    buildJsonArray {
                        element.forEachIndexed { i, item ->
                            if (i == index) add(value)
                            add(item)
                        }
                        if (index == element.size) add(value)
                    }
                } else {
                    if (index !in element.indices) {
                        throw invalidPatch("array index out of bounds: $index")
                    }
                    buildJsonArray {
                        element.forEachIndexed { i, item ->
                            add(if (i == index) add(item, rest, value) else item)
                        }
                    }
                }
            }
            else -> throw invalidPatch("cannot add into non-container")
        }
    }

    private fun remove(element: JsonElement, tokens: List<String>): JsonElement =
        removeReturning(element, tokens).first

    private fun removeReturning(
        element: JsonElement,
        tokens: List<String>,
    ): Pair<JsonElement, JsonElement> {
        if (tokens.isEmpty()) throw invalidPatch("cannot remove document root")
        val head = tokens.first()
        val rest = tokens.drop(1)
        return when (element) {
            is JsonObject -> {
                val child = element[head] ?: throw invalidPatch("path not found: $head")
                if (rest.isEmpty()) {
                    buildJsonObject {
                        element.forEach { (k, v) -> if (k != head) put(k, v) }
                    } to child
                } else {
                    val (updatedChild, removed) = removeReturning(child, rest)
                    buildJsonObject {
                        element.forEach { (k, v) ->
                            put(k, if (k == head) updatedChild else v)
                        }
                    } to removed
                }
            }
            is JsonArray -> {
                val index = head.toIntOrNull()
                    ?: throw invalidPatch("invalid array index: $head")
                if (index !in element.indices) {
                    throw invalidPatch("array index out of bounds: $index")
                }
                if (rest.isEmpty()) {
                    buildJsonArray {
                        element.forEachIndexed { i, item ->
                            if (i != index) add(item)
                        }
                    } to element[index]
                } else {
                    val (updatedChild, removed) = removeReturning(element[index], rest)
                    buildJsonArray {
                        element.forEachIndexed { i, item ->
                            add(if (i == index) updatedChild else item)
                        }
                    } to removed
                }
            }
            else -> throw invalidPatch("cannot remove from non-container")
        }
    }

    private fun replace(
        element: JsonElement,
        tokens: List<String>,
        value: JsonElement,
    ): JsonElement {
        // RFC 6902: replace fails if the target does not exist.
        get(element, tokens) ?: throw invalidPatch("replace path not found")
        return add(remove(element, tokens), tokens, value)
    }

    private fun invalidPatch(message: String): ApiException =
        ApiException.BadRequest(message, code = "invalid_patch")
}

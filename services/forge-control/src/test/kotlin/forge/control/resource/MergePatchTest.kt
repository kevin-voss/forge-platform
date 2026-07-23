package forge.control.resource

import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class MergePatchTest {
    @Test
    fun removesKeyOnNull() {
        val target = buildJsonObject {
            put("a", JsonPrimitive("b"))
            put("c", JsonPrimitive("d"))
        }
        val patch = buildJsonObject {
            put("a", JsonNull)
            put("b", JsonPrimitive("c"))
        }
        val result = MergePatch.apply(target, patch)
        assertFalse(result.containsKey("a"))
        assertEquals(JsonPrimitive("c"), result["b"])
        assertEquals(JsonPrimitive("d"), result["c"])
    }

    @Test
    fun replacesScalars() {
        val target = buildJsonObject { put("size", JsonPrimitive("large")) }
        val patch = buildJsonObject { put("size", JsonPrimitive("small")) }
        assertEquals(
            buildJsonObject { put("size", JsonPrimitive("small")) },
            MergePatch.apply(target, patch),
        )
    }

    @Test
    fun deepMergesNestedObjects() {
        // RFC 7396 example
        val target = buildJsonObject {
            put("title", JsonPrimitive("Goodbye!"))
            put(
                "author",
                buildJsonObject {
                    put("givenName", JsonPrimitive("John"))
                    put("familyName", JsonPrimitive("Doe"))
                },
            )
            put("tags", kotlinx.serialization.json.buildJsonArray {
                add(JsonPrimitive("example"))
                add(JsonPrimitive("sample"))
            })
            put("content", JsonPrimitive("This will be unchanged"))
        }
        val patch = buildJsonObject {
            put("title", JsonPrimitive("Hello!"))
            put(
                "phoneNumber",
                JsonPrimitive("+01-123-456-7890"),
            )
            put(
                "author",
                buildJsonObject {
                    put("familyName", JsonNull)
                },
            )
            put("tags", kotlinx.serialization.json.buildJsonArray {
                add(JsonPrimitive("example"))
            })
        }
        val result = MergePatch.apply(target, patch)
        assertEquals(JsonPrimitive("Hello!"), result["title"])
        assertEquals(JsonPrimitive("+01-123-456-7890"), result["phoneNumber"])
        assertEquals(JsonPrimitive("This will be unchanged"), result["content"])
        val author = result["author"] as JsonObject
        assertEquals(JsonPrimitive("John"), author["givenName"])
        assertFalse(author.containsKey("familyName"))
        assertTrue(result["tags"]!!.toString().contains("example"))
        assertFalse(result["tags"]!!.toString().contains("sample"))
    }
}

package forge.control.resource

import forge.control.http.ApiException
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse

class JsonPatchTest {
    @Test
    fun appliesAddRemoveReplaceTest() {
        val doc = buildJsonObject {
            put("spec", buildJsonObject { put("size", JsonPrimitive("large")) })
            put(
                "metadata",
                buildJsonObject {
                    put("labels", buildJsonObject { put("tier", JsonPrimitive("gold")) })
                    put("annotations", buildJsonObject {})
                },
            )
        }
        val ops = buildJsonArray {
            add(
                buildJsonObject {
                    put("op", JsonPrimitive("replace"))
                    put("path", JsonPrimitive("/spec/size"))
                    put("value", JsonPrimitive("small"))
                },
            )
            add(
                buildJsonObject {
                    put("op", JsonPrimitive("add"))
                    put("path", JsonPrimitive("/metadata/labels/env"))
                    put("value", JsonPrimitive("prod"))
                },
            )
            add(
                buildJsonObject {
                    put("op", JsonPrimitive("remove"))
                    put("path", JsonPrimitive("/metadata/labels/tier"))
                },
            )
            add(
                buildJsonObject {
                    put("op", JsonPrimitive("test"))
                    put("path", JsonPrimitive("/spec/size"))
                    put("value", JsonPrimitive("small"))
                },
            )
        }
        val result = JsonPatch.apply(doc, ops)
        val spec = result["spec"]!!.jsonObjectSafe()
        assertEquals(JsonPrimitive("small"), spec["size"])
        val labels = result["metadata"]!!.jsonObjectSafe()["labels"]!!.jsonObjectSafe()
        assertEquals(JsonPrimitive("prod"), labels["env"])
        assertFalse(labels.containsKey("tier"))
    }

    @Test
    fun rejectsPathOutsideWritableRoots() {
        val doc = buildJsonObject {
            put("spec", buildJsonObject {})
            put(
                "metadata",
                buildJsonObject {
                    put("labels", buildJsonObject {})
                    put("annotations", buildJsonObject {})
                    put("id", JsonPrimitive("wgt_1"))
                },
            )
        }
        val ops = buildJsonArray {
            add(
                buildJsonObject {
                    put("op", JsonPrimitive("replace"))
                    put("path", JsonPrimitive("/metadata/id"))
                    put("value", JsonPrimitive("hijacked"))
                },
            )
        }
        val ex = assertFailsWith<ApiException.BadRequest> {
            JsonPatch.apply(doc, ops)
        }
        assertEquals("patch_path_forbidden", ex.code)
    }

    private fun kotlinx.serialization.json.JsonElement.jsonObjectSafe() =
        this as kotlinx.serialization.json.JsonObject
}

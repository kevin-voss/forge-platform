package forge.control.resource

import forge.control.http.ApiException
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull

class CursorCodecTest {
    @Test
    fun roundTripsNameAndId() {
        val encoded = CursorCodec.encode("sample-2", "wgt_01ABC")
        val decoded = CursorCodec.decode(encoded)!!
        assertEquals("sample-2", decoded.name)
        assertEquals("wgt_01ABC", decoded.id)
    }

    @Test
    fun blankCursorIsNull() {
        assertNull(CursorCodec.decode(null))
        assertNull(CursorCodec.decode(""))
        assertNull(CursorCodec.decode("   "))
    }

    @Test
    fun rejectsTamperedCursor() {
        val err = assertFailsWith<ApiException.BadRequest> {
            CursorCodec.decode("not-valid-base64!!!")
        }
        assertEquals("invalid_cursor", err.code)
    }

    @Test
    fun rejectsDecodedEmptyFields() {
        // base64url of {"name":"","id":"x"}
        val err = assertFailsWith<ApiException.BadRequest> {
            CursorCodec.decode("eyJuYW1lIjoiIiwiaWQiOiJ4In0")
        }
        assertEquals("invalid_cursor", err.code)
    }
}

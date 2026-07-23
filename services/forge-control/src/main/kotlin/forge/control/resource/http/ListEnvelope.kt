package forge.control.resource.http

import forge.control.resource.ResourceEnvelopeResponse
import kotlinx.serialization.Serializable

/** `<Kind>List` response shape for filtered/paginated collection GETs. */
@Serializable
data class ListEnvelope(
    val apiVersion: String,
    val kind: String,
    val resourceVersion: String,
    val items: List<ResourceEnvelopeResponse>,
    val nextCursor: String? = null,
)

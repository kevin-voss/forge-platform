package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"forge.local/services/forge-build/internal/manifest"
)

// WriteValidation sends a 400 validation_error envelope for err.
func WriteValidation(w http.ResponseWriter, err error) {
	requestID := ensureRequestID(w)
	env := ValidationEnvelope(err, requestID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(env)
}

// WriteError sends a JSON error envelope with the given status and code.
func WriteError(w http.ResponseWriter, status int, code, message string, details map[string]string) {
	requestID := ensureRequestID(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Envelope{
		Error: Body{
			Code:      code,
			Message:   message,
			Details:   details,
			RequestID: requestID,
		},
	})
}

// WriteManifestValidation maps a manifest.ValidationError to the shared envelope.
func WriteManifestValidation(w http.ResponseWriter, err error) {
	if ve, ok := manifest.AsValidationError(err); ok {
		WriteValidation(w, ve)
		return
	}
	WriteValidation(w, err)
}

func ensureRequestID(w http.ResponseWriter) string {
	requestID := w.Header().Get("X-Request-Id")
	if requestID == "" {
		requestID = newRequestID()
		w.Header().Set("X-Request-Id", requestID)
	}
	return requestID
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "req_00000000000000000000000000000000"
	}
	return "req_" + hex.EncodeToString(b[:])
}

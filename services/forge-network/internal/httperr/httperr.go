package httperr

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
)

// Envelope is the platform HTTP error shape.
type Envelope struct {
	Error Body `json:"error"`
}

// Body is the inner error object.
type Body struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Details   map[string]string `json:"details,omitempty"`
	RequestID string            `json:"requestId"`
}

// Write sends a JSON error envelope.
func Write(w http.ResponseWriter, status int, code, message string) {
	WriteDetails(w, status, code, message, nil)
}

// WriteDetails sends a JSON error envelope with optional details.
func WriteDetails(w http.ResponseWriter, status int, code, message string, details map[string]string) {
	requestID := w.Header().Get("X-Request-Id")
	if requestID == "" {
		requestID = newRequestID()
		w.Header().Set("X-Request-Id", requestID)
	}
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

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "req_00000000000000000000000000000000"
	}
	return "req_" + hex.EncodeToString(b[:])
}

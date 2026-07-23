package api

import (
	"encoding/json"
	"net/http"
)

type errorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string, details map[string]string) {
	writeJSON(w, status, errorEnvelope{
		Error: errorBody{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}

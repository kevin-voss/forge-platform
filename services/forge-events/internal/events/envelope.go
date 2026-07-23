package events

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Envelope is the standard event wrapper stored as the JetStream message payload.
type Envelope struct {
	ID      string          `json:"id"`
	Subject string          `json:"subject"`
	Time    time.Time       `json:"time"`
	Source  string          `json:"source,omitempty"`
	Data    json.RawMessage `json:"data"`
}

// NewEnvelope builds an envelope with a fresh unique id and UTC timestamp.
func NewEnvelope(subject, source string, data json.RawMessage) Envelope {
	if data == nil {
		data = json.RawMessage("null")
	}
	return Envelope{
		ID:      NewEventID(),
		Subject: subject,
		Time:    time.Now().UTC().Truncate(time.Millisecond),
		Source:  source,
		Data:    data,
	}
}

// NewEventID returns a unique event id (also used as the NATS msg-id).
func NewEventID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Extremely unlikely; fall back to timestamp-shaped id.
		return fmt.Sprintf("evt_%d", time.Now().UnixNano())
	}
	return "evt_" + hex.EncodeToString(b[:])
}

// Marshal serializes the envelope to JSON bytes.
func (e Envelope) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

// UnmarshalEnvelope parses a JetStream message payload into an Envelope.
func UnmarshalEnvelope(raw []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return Envelope{}, fmt.Errorf("envelope json: %w", err)
	}
	if env.ID == "" || env.Subject == "" {
		return Envelope{}, fmt.Errorf("envelope missing id or subject")
	}
	return env, nil
}

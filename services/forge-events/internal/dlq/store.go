package dlq

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// ErrNotFound is returned when a DLQ id is unknown.
var ErrNotFound = errors.New("dlq entry not found")

// Entry is a DLQ message with failure metadata (list + detail).
type Entry struct {
	DLQID           string          `json:"dlq_id"`
	EventID         string          `json:"event_id"`
	OriginalSubject string          `json:"original_subject"`
	Consumer        string          `json:"consumer"`
	DeliveryCount   int             `json:"delivery_count"`
	LastError       string          `json:"last_error,omitempty"`
	FirstFailedAt   time.Time       `json:"first_failed_at"`
	CreatedAt       time.Time       `json:"created_at"`
	Family          string          `json:"family,omitempty"`
	Stream          string          `json:"-"`
	Sequence        uint64          `json:"-"`
	Payload         json.RawMessage `json:"-"`
}

// Detail is the full inspect payload for GET /v1/dlq/{id}.
type Detail struct {
	Entry
	Envelope json.RawMessage `json:"envelope"`
}

// ListFilter selects DLQ entries.
type ListFilter struct {
	Subject  string
	Consumer string
}

// StreamDeleter removes a message from a JetStream stream by sequence.
type StreamDeleter interface {
	DeleteMsg(stream string, seq uint64, opts ...nats.JSOpt) error
}

// Store is an in-memory index of DLQ messages (JetStream is the durable log).
type Store struct {
	mu   sync.RWMutex
	byID map[string]Entry
	js   StreamDeleter
}

// NewStore constructs an empty DLQ index. js may be nil (index-only / tests).
func NewStore(js StreamDeleter) *Store {
	return &Store{
		byID: make(map[string]Entry),
		js:   js,
	}
}

// Put inserts or replaces an entry.
func (s *Store) Put(e Entry) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[e.DLQID] = e
}

// Get returns a DLQ entry by id.
func (s *Store) Get(id string) (Entry, error) {
	if s == nil {
		return Entry{}, ErrNotFound
	}
	id = strings.TrimSpace(id)
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.byID[id]
	if !ok {
		return Entry{}, ErrNotFound
	}
	return e, nil
}

// Detail returns list fields plus the full envelope payload.
func (s *Store) Detail(id string) (Detail, error) {
	e, err := s.Get(id)
	if err != nil {
		return Detail{}, err
	}
	env := e.Payload
	if env == nil {
		env = json.RawMessage("null")
	}
	return Detail{Entry: e, Envelope: env}, nil
}

// List returns entries matching optional subject/consumer filters (newest first).
func (s *Store) List(f ListFilter) []Entry {
	if s == nil {
		return nil
	}
	subject := strings.TrimSpace(f.Subject)
	consumer := strings.TrimSpace(f.Consumer)
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entry, 0, len(s.byID))
	for _, e := range s.byID {
		if subject != "" && e.OriginalSubject != subject {
			continue
		}
		if consumer != "" && e.Consumer != consumer {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].DLQID > out[j].DLQID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

// Delete removes a DLQ entry from the index and, when possible, JetStream.
func (s *Store) Delete(id string) error {
	if s == nil {
		return ErrNotFound
	}
	id = strings.TrimSpace(id)
	s.mu.Lock()
	e, ok := s.byID[id]
	if !ok {
		s.mu.Unlock()
		return ErrNotFound
	}
	delete(s.byID, id)
	s.mu.Unlock()

	if s.js != nil && e.Stream != "" && e.Sequence > 0 {
		if err := s.js.DeleteMsg(e.Stream, e.Sequence); err != nil {
			// Index already dropped (acked); JetStream delete is best-effort.
			if !errors.Is(err, nats.ErrMsgNotFound) && !strings.Contains(err.Error(), "not found") {
				return fmt.Errorf("delete jetstream msg: %w", err)
			}
		}
	}
	return nil
}

// Size returns the number of indexed DLQ entries.
func (s *Store) Size() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byID)
}

// PurgeOlderThan removes entries with CreatedAt before cutoff. Returns count removed.
func (s *Store) PurgeOlderThan(cutoff time.Time) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	ids := make([]Entry, 0)
	for _, e := range s.byID {
		if e.CreatedAt.Before(cutoff) {
			ids = append(ids, e)
		}
	}
	for _, e := range ids {
		delete(s.byID, e.DLQID)
	}
	s.mu.Unlock()

	removed := 0
	for _, e := range ids {
		if s.js != nil && e.Stream != "" && e.Sequence > 0 {
			_ = s.js.DeleteMsg(e.Stream, e.Sequence)
		}
		removed++
	}
	return removed
}

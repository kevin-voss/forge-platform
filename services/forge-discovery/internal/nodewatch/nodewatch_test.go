package nodewatch

import (
	"context"
	"testing"
	"time"
)

type memStore struct {
	calls  []string
	affect int64
}

func (m *memStore) MarkNodeUnready(_ context.Context, nodeID string, _ time.Time) (int64, error) {
	m.calls = append(m.calls, nodeID)
	return m.affect, nil
}

func TestHandleWatchPayloadReachableFalse(t *testing.T) {
	st := &memStore{affect: 3}
	s := &Subscriber{
		Store: st,
		Now:   func() time.Time { return time.Unix(0, 0).UTC() },
	}
	payload := `{
	  "type":"MODIFIED",
	  "resourceVersion":"12",
	  "resource":{
	    "kind":"Node",
	    "metadata":{"id":"node-b","name":"node-b"},
	    "status":{"conditions":[{"type":"Reachable","status":"False","reason":"HeartbeatExpired"}]}
	  }
	}`
	if err := s.HandleWatchPayload(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	if len(st.calls) != 1 || st.calls[0] != "node-b" {
		t.Fatalf("calls = %v", st.calls)
	}
}

func TestHandleWatchPayloadIgnoresReachableTrue(t *testing.T) {
	st := &memStore{affect: 1}
	s := &Subscriber{Store: st, Now: time.Now}
	payload := `{
	  "type":"MODIFIED",
	  "resourceVersion":"13",
	  "resource":{
	    "kind":"Node",
	    "metadata":{"name":"node-b"},
	    "status":{"conditions":[{"type":"Reachable","status":"True"}]}
	  }
	}`
	if err := s.HandleWatchPayload(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	if len(st.calls) != 0 {
		t.Fatalf("unexpected calls %v", st.calls)
	}
}

func TestReachableIsFalse(t *testing.T) {
	if !reachableIsFalse([]byte(`{"conditions":[{"type":"Reachable","status":"False"}]}`)) {
		t.Fatal("expected false reachable")
	}
	if reachableIsFalse([]byte(`{"conditions":[{"type":"Reachable","status":"True"}]}`)) {
		t.Fatal("expected true reachable not to trip")
	}
}

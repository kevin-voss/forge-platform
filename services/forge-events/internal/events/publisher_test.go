package events

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
)

func TestFamilyForSubjectAcceptsKnownRejectsUnknown(t *testing.T) {
	families := []string{"build", "deployment", "runtime", "application", "agent"}
	cases := []struct {
		subject string
		want    string
		ok      bool
	}{
		{"application.crashed", "application", true},
		{"build.completed", "build", true},
		{"runtime.node.offline", "runtime", true},
		{"nope.bad", "", false},
		{"application", "", false},
		{"application.*.x", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, err := FamilyForSubject(tc.subject, families)
		if tc.ok {
			if err != nil {
				t.Fatalf("subject %q: unexpected err %v", tc.subject, err)
			}
			if got != tc.want {
				t.Fatalf("subject %q: family = %q, want %q", tc.subject, got, tc.want)
			}
			continue
		}
		if err == nil {
			t.Fatalf("subject %q: expected error", tc.subject)
		}
	}
}

func TestEnvelopeCarriesFieldsAndUniqueIDs(t *testing.T) {
	data := json.RawMessage(`{"service":"demo"}`)
	a := NewEnvelope("application.crashed", "runtime", data)
	b := NewEnvelope("application.crashed", "runtime", data)
	if a.ID == "" || b.ID == "" {
		t.Fatal("expected non-empty ids")
	}
	if a.ID == b.ID {
		t.Fatalf("expected unique ids, both %q", a.ID)
	}
	if !strings.HasPrefix(a.ID, "evt_") {
		t.Fatalf("id = %q, want evt_ prefix", a.ID)
	}
	if a.Subject != "application.crashed" {
		t.Fatalf("subject = %q", a.Subject)
	}
	if a.Source != "runtime" {
		t.Fatalf("source = %q", a.Source)
	}
	if string(a.Data) != string(data) {
		t.Fatalf("data = %s", a.Data)
	}
	if a.Time.IsZero() {
		t.Fatal("expected non-zero time")
	}
	raw, err := a.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	parsed, err := UnmarshalEnvelope(raw)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.ID != a.ID || parsed.Subject != a.Subject {
		t.Fatalf("roundtrip mismatch: %#v", parsed)
	}
}

func TestPublishRejectsOversizedPayload(t *testing.T) {
	p := NewPublisher(&stubJS{}, []string{"application"}, 32, nil, nil)
	big := json.RawMessage(`"` + strings.Repeat("x", 64) + `"`)
	_, err := p.Publish(context.Background(), PublishRequest{
		Subject: "application.crashed",
		Data:    big,
		Source:  "test",
	})
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("err = %v, want ErrPayloadTooLarge", err)
	}
}

func TestPublishRejectsUnknownSubject(t *testing.T) {
	p := NewPublisher(&stubJS{}, []string{"application"}, 1024, nil, nil)
	_, err := p.Publish(context.Background(), PublishRequest{
		Subject: "nope.bad",
		Data:    json.RawMessage(`{}`),
	})
	if !errors.Is(err, ErrInvalidSubject) {
		t.Fatalf("err = %v, want ErrInvalidSubject", err)
	}
}

type stubSchema struct {
	err error
	n   int
}

func (s *stubSchema) Validate(string, json.RawMessage, int) error {
	s.n++
	return s.err
}

func TestPublishRunsSchemaValidator(t *testing.T) {
	js := &stubJS{ack: &nats.PubAck{Stream: "application", Sequence: 1}}
	p := NewPublisher(js, []string{"application"}, 1024, nil, nil)
	v := &stubSchema{err: errors.New("schema boom")}
	p.SetSchemaValidator(v)
	_, err := p.Publish(context.Background(), PublishRequest{
		Subject: "application.crashed",
		Data:    json.RawMessage(`{"service":"demo"}`),
	})
	if err == nil || err.Error() != "schema boom" {
		t.Fatalf("err = %v", err)
	}
	if v.n != 1 {
		t.Fatalf("Validate calls = %d", v.n)
	}
	if js.last != nil {
		t.Fatal("expected no publish after schema failure")
	}
}

func TestPublishHappyPath(t *testing.T) {
	js := &stubJS{ack: &nats.PubAck{Stream: "application", Sequence: 7}}
	p := NewPublisher(js, []string{"application"}, 1024, nil, nil)
	res, err := p.Publish(context.Background(), PublishRequest{
		Subject: "application.crashed",
		Data:    json.RawMessage(`{"service":"demo","reason":"oom"}`),
		Source:  "runtime",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.Stream != "application" || res.Seq != 7 {
		t.Fatalf("result = %#v", res)
	}
	if !strings.HasPrefix(res.EventID, "evt_") {
		t.Fatalf("event_id = %q", res.EventID)
	}
	if js.last == nil {
		t.Fatal("expected publish msg")
	}
	if js.last.Header.Get(nats.MsgIdHdr) != res.EventID {
		t.Fatalf("msg-id = %q, want %q", js.last.Header.Get(nats.MsgIdHdr), res.EventID)
	}
	env, err := UnmarshalEnvelope(js.last.Data)
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if env.ID != res.EventID || env.Subject != "application.crashed" {
		t.Fatalf("envelope = %#v", env)
	}
}

type stubJS struct {
	ack  *nats.PubAck
	err  error
	last *nats.Msg
}

func (s *stubJS) PublishMsg(msg *nats.Msg, _ ...nats.PubOpt) (*nats.PubAck, error) {
	s.last = msg
	if s.err != nil {
		return nil, s.err
	}
	return s.ack, nil
}

package dns

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mdns "github.com/miekg/dns"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Server is the UDP authoritative/forwarding DNS listener.
type Server struct {
	Addr      string
	Zone      string
	Resolver  *ZoneResolver
	Forwarder *Forwarder
	Log       *slog.Logger
	Tracer    trace.Tracer

	QueriesTotal   metric.Int64Counter
	NXDomainTotal  metric.Int64Counter
	ForwardErrors  metric.Int64Counter

	udp     *mdns.Server
	bound   atomic.Bool
	mu      sync.Mutex
	queryN  atomic.Uint64
}

// ListenAndServe starts the UDP DNS server (blocking).
func (s *Server) ListenAndServe() error {
	if s == nil {
		return fmt.Errorf("dns server: nil")
	}
	addr := s.Addr
	if addr == "" {
		addr = ":5053"
	}
	mux := mdns.NewServeMux()
	mux.HandleFunc(".", s.handle)
	udp := &mdns.Server{Addr: addr, Net: "udp", Handler: mux}
	s.mu.Lock()
	s.udp = udp
	s.mu.Unlock()

	// Mark bound once the packet conn is up; miekg ListenAndServe blocks, so probe bind first.
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("dns listen %s: %w", addr, err)
	}
	s.bound.Store(true)
	udp.PacketConn = pc
	err = udp.ActivateAndServe()
	s.bound.Store(false)
	return err
}

// Shutdown stops the UDP server.
func (s *Server) Shutdown() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	udp := s.udp
	s.mu.Unlock()
	if udp == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return udp.ShutdownContext(ctx)
}

// IsBound reports whether the UDP listener is active.
func (s *Server) IsBound() bool {
	return s != nil && s.bound.Load()
}

// Ready probes the local resolver with a synthetic in-zone query.
func (s *Server) Ready(ctx context.Context) error {
	if s == nil || !s.IsBound() {
		return fmt.Errorf("dns resolver not bound")
	}
	addr := s.Addr
	if addr == "" {
		addr = ":5053"
	}
	// Dial ourselves.
	host := "127.0.0.1"
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		// addr like ":5053"
		port = strings.TrimPrefix(addr, ":")
		if port == addr {
			return fmt.Errorf("dns ready: bad addr %q", addr)
		}
	}
	client := &mdns.Client{Net: "udp", Timeout: 1 * time.Second}
	m := new(mdns.Msg)
	zone := s.zone()
	m.SetQuestion(mdns.Fqdn("__health__."+zone), mdns.TypeA)
	resp, _, err := client.ExchangeContext(ctx, m, net.JoinHostPort(host, port))
	if err != nil {
		return fmt.Errorf("dns synthetic query: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("dns synthetic query: empty response")
	}
	// NXDOMAIN/NOERROR both prove the server answered authoritatively.
	if resp.Rcode != mdns.RcodeNameError && resp.Rcode != mdns.RcodeSuccess {
		return fmt.Errorf("dns synthetic query: rcode %d", resp.Rcode)
	}
	return nil
}

func (s *Server) handle(w mdns.ResponseWriter, r *mdns.Msg) {
	if r == nil || len(r.Question) == 0 {
		m := new(mdns.Msg)
		m.SetRcode(r, mdns.RcodeFormatError)
		_ = w.WriteMsg(m)
		return
	}
	q := r.Question[0]
	ctx := context.Background()
	var span trace.Span
	if s.Tracer != nil {
		// Sample ~1% by query counter to limit volume.
		n := s.queryN.Add(1)
		if n%100 == 1 {
			ctx, span = s.Tracer.Start(ctx, "discovery.dns.resolve")
			defer span.End()
		}
	}

	zoneHit := false
	answerCount := 0
	var resp *mdns.Msg

	if s.Resolver != nil && inZone(q.Name, s.zone()) {
		zoneHit = true
		var msg *mdns.Msg
		msg, zoneHit = s.Resolver.Resolve(ctx, q)
		if msg != nil {
			resp = msg
			resp.Id = r.Id
			resp.Question = r.Question
			answerCount = len(resp.Answer)
			if resp.Rcode == mdns.RcodeNameError {
				s.incNX()
			}
		}
	}

	if !zoneHit {
		if s.Forwarder == nil {
			resp = new(mdns.Msg)
			resp.SetRcode(r, mdns.RcodeServerFailure)
		} else {
			fwd, err := s.Forwarder.Exchange(r)
			if err != nil {
				s.incFwdErr()
				resp = new(mdns.Msg)
				resp.SetRcode(r, mdns.RcodeServerFailure)
				if s.Log != nil {
					s.Log.Warn("dns forward failed",
						"event", "discovery.dns.forward_error",
						"qname", q.Name,
						"error", err.Error(),
					)
				}
			} else {
				resp = fwd
				if resp != nil {
					answerCount = len(resp.Answer)
				}
			}
		}
	}

	if resp == nil {
		resp = new(mdns.Msg)
		resp.SetRcode(r, mdns.RcodeServerFailure)
	}
	_ = w.WriteMsg(resp)
	s.incQuery(zoneHit)

	if s.Log != nil {
		n := s.queryN.Load()
		if n%50 == 1 {
			s.Log.Info("dns query",
				"event", "discovery.dns.query",
				"qname", q.Name,
				"qtype", mdns.TypeToString[q.Qtype],
				"answer_count", answerCount,
				"zone_hit", zoneHit,
			)
		}
	}
	if span != nil {
		span.SetAttributes(
			attribute.String("dns.qname", q.Name),
			attribute.Bool("dns.zone_hit", zoneHit),
			attribute.Int("dns.answer_count", answerCount),
		)
	}
}

func (s *Server) zone() string {
	zone := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s.Zone)), ".")
	if zone == "" {
		return "svc.forge"
	}
	return zone
}

func (s *Server) incQuery(zoneHit bool) {
	if s.QueriesTotal == nil {
		return
	}
	s.QueriesTotal.Add(context.Background(), 1, metric.WithAttributes(
		attribute.Bool("zone_hit", zoneHit),
	))
}

func (s *Server) incNX() {
	if s.NXDomainTotal == nil {
		return
	}
	s.NXDomainTotal.Add(context.Background(), 1)
}

func (s *Server) incFwdErr() {
	if s.ForwardErrors == nil {
		return
	}
	s.ForwardErrors.Add(context.Background(), 1)
}

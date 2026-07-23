package dns

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"forge.local/services/forge-discovery/internal/store"
	mdns "github.com/miekg/dns"
)

// EndpointStore is the store surface ZoneResolver needs.
type EndpointStore interface {
	LookupServiceByNameOrAlias(ctx context.Context, project, environment, nameOrAlias string) (store.ServiceRow, error)
	ListEndpoints(ctx context.Context, f store.ListFilter) ([]store.EndpointRow, error)
	GetEndpoint(ctx context.Context, project, environment, id string) (store.EndpointRow, error)
}

// OwnerName is a parsed svc.forge query owner.
type OwnerName struct {
	Kind        OwnerKind
	Service     string
	Environment string
	Project     string
	EndpointID  string
	PortName    string
	Protocol    string // DNS SRV protocol label (tcp/udp), without underscore
}

// OwnerKind classifies a parsed zone name.
type OwnerKind int

const (
	OwnerInvalid OwnerKind = iota
	OwnerService
	OwnerEndpoint
	OwnerSRV
)

// ParseOwnerName extracts service/environment/project (and optional SRV/endpoint labels).
// Zone should be like "svc.forge" (no trailing dot).
func ParseOwnerName(qname, zone string) (OwnerName, bool) {
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(qname)), ".")
	zone = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(zone)), ".")
	if name == "" || zone == "" {
		return OwnerName{}, false
	}
	if name == zone {
		return OwnerName{}, false
	}
	if !strings.HasSuffix(name, "."+zone) {
		return OwnerName{}, false
	}
	prefix := strings.TrimSuffix(name, "."+zone)
	labels := strings.Split(prefix, ".")
	// <service>.<environment>.<project>
	if len(labels) == 3 {
		return OwnerName{
			Kind:        OwnerService,
			Service:     labels[0],
			Environment: labels[1],
			Project:     labels[2],
		}, true
	}
	// <endpoint-id>.<service>.<environment>.<project>
	if len(labels) == 4 {
		return OwnerName{
			Kind:        OwnerEndpoint,
			EndpointID:  labels[0],
			Service:     labels[1],
			Environment: labels[2],
			Project:     labels[3],
		}, true
	}
	// _<port>._<proto>.<service>.<environment>.<project>
	if len(labels) == 5 && strings.HasPrefix(labels[0], "_") && strings.HasPrefix(labels[1], "_") {
		return OwnerName{
			Kind:        OwnerSRV,
			PortName:    strings.TrimPrefix(labels[0], "_"),
			Protocol:    strings.TrimPrefix(labels[1], "_"),
			Service:     labels[2],
			Environment: labels[3],
			Project:     labels[4],
		}, true
	}
	return OwnerName{}, false
}

// ZoneResolver answers authoritative queries for the configured zone.
type ZoneResolver struct {
	Store  EndpointStore
	Zone   string
	TTL    TTLPolicy
	Now    func() time.Time
}

// Resolve builds a DNS reply for an in-zone question. ok=false means the name is not in-zone.
func (z *ZoneResolver) Resolve(ctx context.Context, q mdns.Question) (msg *mdns.Msg, zoneHit bool) {
	zone := z.zone()
	qname := strings.ToLower(q.Name)
	if !inZone(qname, zone) {
		return nil, false
	}

	out := new(mdns.Msg)
	out.SetReply(&mdns.Msg{Question: []mdns.Question{q}})
	out.Authoritative = true
	out.RecursionAvailable = true

	owner, ok := ParseOwnerName(qname, zone)
	if !ok {
		z.writeNXDOMAIN(out, q)
		return out, true
	}

	now := time.Now().UTC()
	if z.Now != nil {
		now = z.Now().UTC()
	}

	svc, err := z.Store.LookupServiceByNameOrAlias(ctx, owner.Project, owner.Environment, owner.Service)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			z.writeNXDOMAIN(out, q)
			return out, true
		}
		out.Rcode = mdns.RcodeServerFailure
		return out, true
	}
	// Canonical service name for answer owner/target construction.
	canonService := svc.Name

	switch owner.Kind {
	case OwnerService:
		z.answerService(ctx, out, q, owner, canonService, now)
	case OwnerEndpoint:
		z.answerEndpoint(ctx, out, q, owner, canonService, now)
	case OwnerSRV:
		z.answerSRV(ctx, out, q, owner, canonService, now)
	default:
		z.writeNXDOMAIN(out, q)
	}
	return out, true
}

func (z *ZoneResolver) answerService(ctx context.Context, out *mdns.Msg, q mdns.Question, owner OwnerName, canonService string, now time.Time) {
	if q.Qtype != mdns.TypeA && q.Qtype != mdns.TypeAAAA && q.Qtype != mdns.TypeANY {
		// No data for other types — empty NOERROR with SOA-like negative TTL via SOA not required;
		// use NXDOMAIN only when the name has no Ready endpoints for address queries.
		if q.Qtype == mdns.TypeSRV {
			z.writeNXDOMAIN(out, q)
			return
		}
		out.Rcode = mdns.RcodeSuccess
		return
	}
	eps, err := z.Store.ListEndpoints(ctx, store.ListFilter{
		Project: owner.Project, Environment: owner.Environment, Service: canonService, ReadyOnly: true,
	})
	if err != nil {
		out.Rcode = mdns.RcodeServerFailure
		return
	}
	if len(eps) == 0 {
		z.writeNXDOMAIN(out, q)
		return
	}
	ownerFQDN := mdns.Fqdn(q.Name)
	for _, ep := range eps {
		ttl := z.TTL.AnswerTTL(ep.ExpiresAt, now)
		ip := net.ParseIP(ep.AddressIP)
		if ip == nil {
			continue
		}
		if ip4 := ip.To4(); ip4 != nil {
			if q.Qtype == mdns.TypeA || q.Qtype == mdns.TypeANY {
				out.Answer = append(out.Answer, &mdns.A{
					Hdr: mdns.RR_Header{Name: ownerFQDN, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: ttl},
					A:   ip4,
				})
			}
		} else if q.Qtype == mdns.TypeAAAA || q.Qtype == mdns.TypeANY {
			out.Answer = append(out.Answer, &mdns.AAAA{
				Hdr:  mdns.RR_Header{Name: ownerFQDN, Rrtype: mdns.TypeAAAA, Class: mdns.ClassINET, Ttl: ttl},
				AAAA: ip,
			})
		}
	}
	if len(out.Answer) == 0 {
		z.writeNXDOMAIN(out, q)
	}
}

func (z *ZoneResolver) answerEndpoint(ctx context.Context, out *mdns.Msg, q mdns.Question, owner OwnerName, canonService string, now time.Time) {
	if q.Qtype != mdns.TypeA && q.Qtype != mdns.TypeAAAA && q.Qtype != mdns.TypeANY {
		out.Rcode = mdns.RcodeSuccess
		return
	}
	ep, err := z.Store.GetEndpoint(ctx, owner.Project, owner.Environment, owner.EndpointID)
	if err != nil || ep.Service != canonService || ep.Phase != "Ready" {
		z.writeNXDOMAIN(out, q)
		return
	}
	ttl := z.TTL.AnswerTTL(ep.ExpiresAt, now)
	ip := net.ParseIP(ep.AddressIP)
	if ip == nil {
		z.writeNXDOMAIN(out, q)
		return
	}
	ownerFQDN := mdns.Fqdn(q.Name)
	if ip4 := ip.To4(); ip4 != nil {
		if q.Qtype == mdns.TypeA || q.Qtype == mdns.TypeANY {
			out.Answer = append(out.Answer, &mdns.A{
				Hdr: mdns.RR_Header{Name: ownerFQDN, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: ttl},
				A:   ip4,
			})
		}
	} else if q.Qtype == mdns.TypeAAAA || q.Qtype == mdns.TypeANY {
		out.Answer = append(out.Answer, &mdns.AAAA{
			Hdr:  mdns.RR_Header{Name: ownerFQDN, Rrtype: mdns.TypeAAAA, Class: mdns.ClassINET, Ttl: ttl},
			AAAA: ip,
		})
	}
	if len(out.Answer) == 0 {
		z.writeNXDOMAIN(out, q)
	}
}

func (z *ZoneResolver) answerSRV(ctx context.Context, out *mdns.Msg, q mdns.Question, owner OwnerName, canonService string, now time.Time) {
	if q.Qtype != mdns.TypeSRV && q.Qtype != mdns.TypeANY {
		out.Rcode = mdns.RcodeSuccess
		return
	}
	eps, err := z.Store.ListEndpoints(ctx, store.ListFilter{
		Project: owner.Project, Environment: owner.Environment, Service: canonService, ReadyOnly: true,
	})
	if err != nil {
		out.Rcode = mdns.RcodeServerFailure
		return
	}
	if len(eps) == 0 {
		z.writeNXDOMAIN(out, q)
		return
	}

	ownerFQDN := mdns.Fqdn(q.Name)
	zone := z.zone()
	matched := 0
	for _, ep := range eps {
		port, ok := matchSRVPort(owner, ep)
		if !ok {
			continue
		}
		ttl := z.TTL.AnswerTTL(ep.ExpiresAt, now)
		target := mdns.Fqdn(ep.ID + "." + canonService + "." + owner.Environment + "." + owner.Project + "." + zone)
		out.Answer = append(out.Answer, &mdns.SRV{
			Hdr:      mdns.RR_Header{Name: ownerFQDN, Rrtype: mdns.TypeSRV, Class: mdns.ClassINET, Ttl: ttl},
			Priority: 10,
			Weight:   10,
			Port:     uint16(port),
			Target:   target,
		})
		// Glue A/AAAA in additional section.
		if ip := net.ParseIP(ep.AddressIP); ip != nil {
			if ip4 := ip.To4(); ip4 != nil {
				out.Extra = append(out.Extra, &mdns.A{
					Hdr: mdns.RR_Header{Name: target, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: ttl},
					A:   ip4,
				})
			} else {
				out.Extra = append(out.Extra, &mdns.AAAA{
					Hdr:  mdns.RR_Header{Name: target, Rrtype: mdns.TypeAAAA, Class: mdns.ClassINET, Ttl: ttl},
					AAAA: ip,
				})
			}
		}
		matched++
	}
	if matched == 0 {
		z.writeNXDOMAIN(out, q)
	}
}

func matchSRVPort(owner OwnerName, ep store.EndpointRow) (int, bool) {
	// DNS SRV protocol label is tcp/udp; endpoint.protocol is app protocol (http, grpc, …).
	proto := strings.ToLower(owner.Protocol)
	if proto != "tcp" && proto != "udp" {
		return 0, false
	}
	portName := strings.ToLower(owner.PortName)
	appProto := strings.ToLower(ep.Protocol)
	if appProto == "" {
		appProto = "http"
	}
	// Match when port name equals the endpoint's application protocol (common case: _http._tcp).
	if portName == appProto {
		return ep.AddressPort, true
	}
	// Also accept literal "tcp"/"udp" as port name mapping to the listen port.
	if portName == proto {
		return ep.AddressPort, true
	}
	return 0, false
}

func (z *ZoneResolver) writeNXDOMAIN(out *mdns.Msg, q mdns.Question) {
	out.Rcode = mdns.RcodeNameError
	out.Answer = nil
	// SOA in authority for negative caching TTL.
	zone := mdns.Fqdn(z.zone())
	neg := z.TTL.NegTTL()
	out.Ns = []mdns.RR{
		&mdns.SOA{
			Hdr:     mdns.RR_Header{Name: zone, Rrtype: mdns.TypeSOA, Class: mdns.ClassINET, Ttl: neg},
			Ns:      "ns." + zone,
			Mbox:    "hostmaster." + zone,
			Serial:  1,
			Refresh: 30,
			Retry:   10,
			Expire:  300,
			Minttl:  neg,
		},
	}
	_ = q
}

func (z *ZoneResolver) zone() string {
	zone := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(z.Zone)), ".")
	if zone == "" {
		return "svc.forge"
	}
	return zone
}

func inZone(qname, zone string) bool {
	qname = strings.TrimSuffix(strings.ToLower(qname), ".")
	zone = strings.TrimSuffix(strings.ToLower(zone), ".")
	return qname == zone || strings.HasSuffix(qname, "."+zone)
}

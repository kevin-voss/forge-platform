package dns

import (
	"fmt"
	"net"
	"strings"
	"time"

	mdns "github.com/miekg/dns"
)

// Forwarder passes non-zone queries to an upstream resolver (UDP).
type Forwarder struct {
	Upstream string
	Timeout  time.Duration
	Client   *mdns.Client
}

// Exchange forwards msg to the configured upstream and returns the response.
func (f *Forwarder) Exchange(msg *mdns.Msg) (*mdns.Msg, error) {
	if f == nil || f.Upstream == "" {
		return nil, fmt.Errorf("dns forwarder: no upstream")
	}
	client := f.Client
	if client == nil {
		client = &mdns.Client{Net: "udp", Timeout: f.timeout()}
	} else if client.Timeout == 0 {
		client.Timeout = f.timeout()
	}
	upstream := ensureDNSPort(f.Upstream)
	resp, _, err := client.Exchange(msg, upstream)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (f *Forwarder) timeout() time.Duration {
	if f.Timeout > 0 {
		return f.Timeout
	}
	return 2 * time.Second
}

func ensureDNSPort(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "127.0.0.11:53"
	}
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	// IPv6 without port.
	if strings.Contains(addr, ":") && !strings.HasPrefix(addr, "[") {
		return "[" + addr + "]:53"
	}
	return net.JoinHostPort(addr, "53")
}

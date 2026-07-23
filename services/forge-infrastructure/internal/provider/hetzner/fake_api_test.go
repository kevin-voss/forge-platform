package hetzner

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// fakeAPI is an in-memory Hetzner API for unit/integration tests.
type fakeAPI struct {
	mu sync.Mutex

	servers     map[int64]*Server
	networks    map[int64]*Network
	volumes     map[int64]*Volume
	floatingIPs map[int64]*FloatingIP
	locations   []Location
	serverTypes []ServerType

	nextID int64
	calls  []string

	// createDelay simulates slow CreateServer for concurrency tests.
	createDelay time.Duration
	// failCreatesBefore lets N CreateServer calls fail (then succeed).
	failCreatesBefore int
	createAttempts    int
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{
		servers:     map[int64]*Server{},
		networks:    map[int64]*Network{},
		volumes:     map[int64]*Volume{},
		floatingIPs: map[int64]*FloatingIP{},
		locations: []Location{
			{ID: 1, Name: "fsn1", City: "Falkenstein"},
			{ID: 2, Name: "nbg1", City: "Nuremberg"},
		},
		serverTypes: []ServerType{
			{ID: 1, Name: "cx22", Cores: 2, Memory: 4, Disk: 40, Prices: []STPrice{{
				Location: "fsn1",
				PriceHourly: struct {
					Gross string `json:"gross"`
					Net   string `json:"net"`
				}{Gross: "0.0060", Net: "0.0050"},
			}}},
			{ID: 2, Name: "cx32", Cores: 4, Memory: 8, Disk: 80, Prices: []STPrice{{
				Location: "fsn1",
				PriceHourly: struct {
					Gross string `json:"gross"`
					Net   string `json:"net"`
				}{Gross: "0.0120", Net: "0.0100"},
			}}},
		},
		nextID: 1000,
	}
}

func (f *fakeAPI) alloc() int64 {
	f.nextID++
	return f.nextID
}

func (f *fakeAPI) record(name string) {
	f.calls = append(f.calls, name)
}

func (f *fakeAPI) CallOrder() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func matchLabels(labels map[string]string, selector string) bool {
	if selector == "" {
		return true
	}
	// Minimal parser: comma-separated key=value or key==value
	parts := splitSelector(selector)
	for _, p := range parts {
		key, val, ok := splitKV(p)
		if !ok {
			continue
		}
		if labels == nil || labels[key] != val {
			return false
		}
	}
	return true
}

func splitSelector(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func splitKV(p string) (string, string, bool) {
	for i := 0; i < len(p); i++ {
		if p[i] == '=' {
			key := p[:i]
			val := p[i+1:]
			if len(val) > 0 && val[0] == '=' {
				val = val[1:]
			}
			return key, val, true
		}
	}
	return "", "", false
}

func (f *fakeAPI) ListServers(ctx context.Context, labelSelector string) ([]Server, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("server.list")
	out := make([]Server, 0)
	for _, s := range f.servers {
		if matchLabels(s.Labels, labelSelector) {
			cp := *s
			out = append(out, cp)
		}
	}
	return out, nil
}

func (f *fakeAPI) GetServer(ctx context.Context, id int64) (*Server, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("server.get")
	s, ok := f.servers[id]
	if !ok {
		return nil, &notFoundError{path: fmt.Sprintf("/servers/%d", id)}
	}
	cp := *s
	return &cp, nil
}

func (f *fakeAPI) CreateServer(ctx context.Context, req CreateServerRequest) (*Server, error) {
	if f.createDelay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.createDelay):
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createAttempts++
	if f.failCreatesBefore > 0 && f.createAttempts <= f.failCreatesBefore {
		return nil, fmt.Errorf("simulated create failure")
	}
	f.record("server.create")
	id := f.alloc()
	s := &Server{
		ID:         id,
		Name:       req.Name,
		Status:     "running",
		Created:    time.Now().UTC().Format(time.RFC3339),
		ServerType: &ServerType{Name: req.ServerType, Cores: 2, Memory: 4},
		Datacenter: &Datacenter{Location: &Location{Name: req.Location}},
		PublicNet:  &PublicNet{IPv4: &IPv4{IP: fmt.Sprintf("5.161.%d.%d", id%200, id%250)}},
		Labels:     req.Labels,
	}
	f.servers[id] = s
	cp := *s
	return &cp, nil
}

func (f *fakeAPI) DeleteServer(ctx context.Context, id int64) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("server.delete")
	if _, ok := f.servers[id]; !ok {
		return &notFoundError{path: fmt.Sprintf("/servers/%d", id)}
	}
	delete(f.servers, id)
	return nil
}

func (f *fakeAPI) RebootServer(ctx context.Context, id int64) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("server.reboot")
	if _, ok := f.servers[id]; !ok {
		return &notFoundError{path: fmt.Sprintf("/servers/%d", id)}
	}
	return nil
}

func (f *fakeAPI) ListNetworks(ctx context.Context, labelSelector string) ([]Network, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("network.list")
	out := []Network{}
	for _, n := range f.networks {
		if matchLabels(n.Labels, labelSelector) {
			out = append(out, *n)
		}
	}
	return out, nil
}

func (f *fakeAPI) CreateNetwork(ctx context.Context, req CreateNetworkAPIRequest) (*Network, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("network.create")
	id := f.alloc()
	n := &Network{ID: id, Name: req.Name, IPRange: req.IPRange, Labels: req.Labels}
	f.networks[id] = n
	cp := *n
	return &cp, nil
}

func (f *fakeAPI) DeleteNetwork(ctx context.Context, id int64) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("network.delete")
	delete(f.networks, id)
	return nil
}

func (f *fakeAPI) ListVolumes(ctx context.Context, labelSelector string) ([]Volume, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("volume.list")
	out := []Volume{}
	for _, v := range f.volumes {
		if matchLabels(v.Labels, labelSelector) {
			out = append(out, *v)
		}
	}
	return out, nil
}

func (f *fakeAPI) CreateVolume(ctx context.Context, req CreateVolumeRequest) (*Volume, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("volume.create")
	id := f.alloc()
	var server *int64
	if req.Server != nil {
		s := *req.Server
		server = &s
	}
	v := &Volume{
		ID: id, Name: req.Name, Size: req.Size, Server: server, Labels: req.Labels,
		Location: &Location{Name: req.Location},
	}
	f.volumes[id] = v
	cp := *v
	return &cp, nil
}

func (f *fakeAPI) AttachVolume(ctx context.Context, volumeID, serverID int64) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("volume.attach")
	v, ok := f.volumes[volumeID]
	if !ok {
		return &notFoundError{path: fmt.Sprintf("/volumes/%d", volumeID)}
	}
	s := serverID
	v.Server = &s
	return nil
}

func (f *fakeAPI) DetachVolume(ctx context.Context, volumeID int64) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("volume.detach")
	v, ok := f.volumes[volumeID]
	if !ok {
		return &notFoundError{path: fmt.Sprintf("/volumes/%d", volumeID)}
	}
	v.Server = nil
	return nil
}

func (f *fakeAPI) ResizeVolume(ctx context.Context, volumeID int64, sizeGiB int) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("volume.resize")
	v, ok := f.volumes[volumeID]
	if !ok {
		return &notFoundError{path: fmt.Sprintf("/volumes/%d", volumeID)}
	}
	v.Size = sizeGiB
	return nil
}

func (f *fakeAPI) DeleteVolume(ctx context.Context, id int64) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("volume.delete")
	delete(f.volumes, id)
	return nil
}

func (f *fakeAPI) ListFloatingIPs(ctx context.Context, labelSelector string) ([]FloatingIP, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("floating_ip.list")
	out := []FloatingIP{}
	for _, ip := range f.floatingIPs {
		if matchLabels(ip.Labels, labelSelector) {
			out = append(out, *ip)
		}
	}
	return out, nil
}

func (f *fakeAPI) CreateFloatingIP(ctx context.Context, req CreateFloatingIPRequest) (*FloatingIP, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("floating_ip.create")
	id := f.alloc()
	var server *int64
	if req.Server != nil {
		s := *req.Server
		server = &s
	}
	ip := &FloatingIP{
		ID: id, IP: fmt.Sprintf("49.12.%d.%d", id%200, id%250), Type: "ipv4",
		Server: server, Labels: req.Labels,
		HomeLocation: &Location{Name: req.HomeLocation},
	}
	f.floatingIPs[id] = ip
	cp := *ip
	return &cp, nil
}

func (f *fakeAPI) AssignFloatingIP(ctx context.Context, ipID, serverID int64) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("floating_ip.assign")
	ip, ok := f.floatingIPs[ipID]
	if !ok {
		return &notFoundError{path: fmt.Sprintf("/floating_ips/%d", ipID)}
	}
	s := serverID
	ip.Server = &s
	return nil
}

func (f *fakeAPI) UnassignFloatingIP(ctx context.Context, ipID int64) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("floating_ip.unassign")
	ip, ok := f.floatingIPs[ipID]
	if !ok {
		return &notFoundError{path: fmt.Sprintf("/floating_ips/%d", ipID)}
	}
	ip.Server = nil
	return nil
}

func (f *fakeAPI) DeleteFloatingIP(ctx context.Context, id int64) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("floating_ip.delete")
	delete(f.floatingIPs, id)
	return nil
}

func (f *fakeAPI) ListLocations(ctx context.Context) ([]Location, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Location(nil), f.locations...), nil
}

func (f *fakeAPI) ListServerTypes(ctx context.Context) ([]ServerType, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ServerType(nil), f.serverTypes...), nil
}

func (f *fakeAPI) serverCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.servers)
}

func (f *fakeAPI) volumeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.volumes)
}

func (f *fakeAPI) floatingCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.floatingIPs)
}

func (f *fakeAPI) networkCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.networks)
}

package docker

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type fakeEngine struct {
	mu         sync.Mutex
	containers map[string]*fakeContainer
	volumes    map[string]map[string]string
	networks   map[string]*NetworkInspect
	pingErr    error
	failCreate bool
	failStart  bool
}

type fakeContainer struct {
	id      string
	name    string
	cfg     ContainerConfig
	running bool
	ip      string
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{
		containers: map[string]*fakeContainer{},
		volumes:    map[string]map[string]string{},
		networks: map[string]*NetworkInspect{
			"forge-platform_default": {ID: "net-default", Name: "forge-platform_default"},
			"net-default":            {ID: "net-default", Name: "forge-platform_default"},
		},
	}
}

func (f *fakeEngine) Ping(ctx context.Context) error {
	return f.pingErr
}

func (f *fakeEngine) ContainerCreate(ctx context.Context, name string, cfg ContainerConfig) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failCreate {
		return "", fmt.Errorf("create failed")
	}
	for _, c := range f.containers {
		if c.name == name {
			return "", fmt.Errorf("docker create: status 409: name in use")
		}
	}
	id := fmt.Sprintf("%032x", len(f.containers)+1)
	// Mix in name so ids stay unique even if create order is reused in tests.
	sum := 0
	for _, r := range name {
		sum += int(r)
	}
	id = fmt.Sprintf("%012x%020x", len(f.containers)+1+sum, len(f.containers)+1)
	f.containers[id] = &fakeContainer{
		id:   id,
		name: name,
		cfg:  cfg,
		ip:   fmt.Sprintf("172.20.0.%d", 10+len(f.containers)),
	}
	return id, nil
}

func (f *fakeEngine) ContainerStart(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failStart {
		return fmt.Errorf("start failed")
	}
	c, ok := f.containers[id]
	if !ok {
		return fmt.Errorf("docker start: status 404")
	}
	c.running = true
	return nil
}

func (f *fakeEngine) ContainerStop(ctx context.Context, id string, timeoutSec int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.containers[id]; ok {
		c.running = false
	}
	return nil
}

func (f *fakeEngine) ContainerRemove(ctx context.Context, id string, force bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.containers, id)
	return nil
}

func (f *fakeEngine) ContainerRestart(ctx context.Context, id string, timeoutSec int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.containers[id]
	if !ok {
		return fmt.Errorf("docker restart: status 404")
	}
	c.running = true
	return nil
}

func (f *fakeEngine) ContainerInspect(ctx context.Context, id string) (*ContainerInspect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.containers[id]
	if !ok {
		// allow short prefix match
		for full, fc := range f.containers {
			if strings.HasPrefix(full, id) {
				c = fc
				ok = true
				break
			}
		}
	}
	if !ok {
		return nil, fmt.Errorf("docker inspect: status 404")
	}
	status := "created"
	if c.running {
		status = "running"
	}
	return &ContainerInspect{
		ID:   c.id,
		Name: c.name,
		State: ContainerState{
			Status:  status,
			Running: c.running,
		},
		Config: InspectConfig{
			Labels: c.cfg.Labels,
			Image:  c.cfg.Image,
			Env:    c.cfg.Env,
		},
		NetworkSettings: NetworkSettings{
			IPAddress: c.ip,
			Networks: map[string]EndpointSettings{
				c.cfg.Network: {IPAddress: c.ip},
			},
		},
	}, nil
}

func (f *fakeEngine) ContainerList(ctx context.Context, filters map[string][]string, all bool) ([]ContainerSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	want := map[string]string{}
	if labels, ok := filters["label"]; ok {
		for _, l := range labels {
			parts := strings.SplitN(l, "=", 2)
			if len(parts) == 2 {
				want[parts[0]] = parts[1]
			}
		}
	}
	out := []ContainerSummary{}
	for _, c := range f.containers {
		if !all && !c.running {
			continue
		}
		match := true
		for k, v := range want {
			if c.cfg.Labels[k] != v {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		state := "created"
		if c.running {
			state = "running"
		}
		out = append(out, ContainerSummary{
			ID:     c.id,
			Names:  []string{c.name},
			Labels: c.cfg.Labels,
			State:  state,
		})
	}
	return out, nil
}

func (f *fakeEngine) VolumeCreate(ctx context.Context, name string, labels map[string]string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.volumes[name] = labels
	return name, nil
}

func (f *fakeEngine) VolumeRemove(ctx context.Context, name string, force bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.volumes, name)
	return nil
}

func (f *fakeEngine) NetworkCreate(ctx context.Context, name string, labels map[string]string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := "net-" + name
	f.networks[name] = &NetworkInspect{ID: id, Name: name, Labels: labels}
	f.networks[id] = f.networks[name]
	return id, nil
}

func (f *fakeEngine) NetworkRemove(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n, ok := f.networks[id]; ok {
		delete(f.networks, id)
		delete(f.networks, n.Name)
	}
	return nil
}

func (f *fakeEngine) NetworkInspect(ctx context.Context, idOrName string) (*NetworkInspect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n, ok := f.networks[idOrName]; ok {
		return n, nil
	}
	return nil, fmt.Errorf("docker network inspect: status 404")
}

func (f *fakeEngine) Close() error { return nil }

func (f *fakeEngine) volumeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.volumes)
}

func (f *fakeEngine) containerCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.containers)
}

type memKnown struct {
	ids map[string]struct{}
}

func (m *memKnown) ProviderNodeIDs(ctx context.Context) (map[string]struct{}, error) {
	out := make(map[string]struct{}, len(m.ids))
	for k := range m.ids {
		out[k] = struct{}{}
	}
	return out, nil
}

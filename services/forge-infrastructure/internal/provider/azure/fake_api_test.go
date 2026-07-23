package azure

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type fakeAPI struct {
	mu sync.Mutex

	vms   map[string]*VM
	vnets map[string]*VNet
	disks map[string]*Disk
	ips   map[string]*PublicIPAddr

	locations []Location
	sizes     []VMSize

	nextN       int
	calls       []string
	createDelay time.Duration
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{
		vms:       map[string]*VM{},
		vnets:     map[string]*VNet{},
		disks:     map[string]*Disk{},
		ips:       map[string]*PublicIPAddr{},
		locations: defaultLocations(),
		sizes:     defaultVMSizes(),
		nextN:     1000,
	}
}

func (f *fakeAPI) alloc(prefix string) string {
	f.nextN++
	return fmt.Sprintf("%s%d", prefix, f.nextN)
}

func (f *fakeAPI) record(name string) { f.calls = append(f.calls, name) }

func (f *fakeAPI) CallOrder() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeAPI) ListLocations(ctx context.Context) ([]Location, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("locations.list")
	return append([]Location(nil), f.locations...), nil
}

func (f *fakeAPI) ListVMSizes(ctx context.Context, location string) ([]VMSize, error) {
	_ = ctx
	_ = location
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("vmsizes.list")
	return append([]VMSize(nil), f.sizes...), nil
}

func (f *fakeAPI) ListVMs(ctx context.Context, tags map[string]string) ([]VM, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("vm.list")
	out := make([]VM, 0)
	for _, v := range f.vms {
		if matchTags(v.Tags, tags) {
			cp := *v
			out = append(out, cp)
		}
	}
	return out, nil
}

func (f *fakeAPI) GetVM(ctx context.Context, vmID string) (*VM, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("vm.get")
	v, ok := f.vms[vmID]
	if !ok {
		return nil, &notFoundError{path: vmID}
	}
	cp := *v
	return &cp, nil
}

func (f *fakeAPI) CreateVM(ctx context.Context, req CreateVMRequest) (*VM, error) {
	_ = ctx
	if f.createDelay > 0 {
		time.Sleep(f.createDelay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, v := range f.vms {
		if req.Tags != nil && v.Tags != nil && v.Tags[TagOpID] != "" && v.Tags[TagOpID] == req.Tags[TagOpID] {
			f.record("vm.create")
			cp := *v
			return &cp, nil
		}
	}
	id := f.alloc("vm-")
	tags := map[string]string{}
	for k, v := range req.Tags {
		tags[k] = v
	}
	vm := &VM{
		ID: id, Name: req.Name, Location: req.Location, Size: req.Size,
		PrivateIP: fmt.Sprintf("10.40.1.%d", f.nextN%250+2),
		PublicIP:  fmt.Sprintf("20.%d.%d.%d", f.nextN%200, f.nextN%200, f.nextN%200),
		PowerState: "running", Tags: tags, Created: time.Now().UTC(),
	}
	f.vms[id] = vm
	f.record("vm.create")
	cp := *vm
	return &cp, nil
}

func (f *fakeAPI) DeleteVM(ctx context.Context, vmID string) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("vm.delete")
	if _, ok := f.vms[vmID]; !ok {
		return &notFoundError{path: vmID}
	}
	delete(f.vms, vmID)
	return nil
}

func (f *fakeAPI) RestartVM(ctx context.Context, vmID string) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("vm.restart")
	if _, ok := f.vms[vmID]; !ok {
		return &notFoundError{path: vmID}
	}
	return nil
}

func (f *fakeAPI) CreateVNet(ctx context.Context, req CreateVNetRequest) (*VNet, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.alloc("vnet-")
	tags := map[string]string{}
	for k, v := range req.Tags {
		tags[k] = v
	}
	v := &VNet{
		ID: id, Name: req.Name, Location: req.Location, CIDR: req.CIDR,
		Tags: tags, SubnetID: f.alloc("subnet-"), NSGID: f.alloc("nsg-"),
	}
	f.vnets[id] = v
	f.record("vnet.create")
	cp := *v
	return &cp, nil
}

func (f *fakeAPI) DeleteVNet(ctx context.Context, vnetID string) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("vnet.delete")
	if _, ok := f.vnets[vnetID]; !ok {
		return &notFoundError{path: vnetID}
	}
	delete(f.vnets, vnetID)
	return nil
}

func (f *fakeAPI) ListVNets(ctx context.Context, tags map[string]string) ([]VNet, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("vnet.list")
	out := make([]VNet, 0)
	for _, v := range f.vnets {
		if matchTags(v.Tags, tags) {
			cp := *v
			out = append(out, cp)
		}
	}
	return out, nil
}

func (f *fakeAPI) CreateDisk(ctx context.Context, req CreateDiskRequest) (*Disk, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.alloc("disk-")
	tags := map[string]string{}
	for k, v := range req.Tags {
		tags[k] = v
	}
	d := &Disk{ID: id, Name: req.Name, SizeGiB: req.SizeGiB, VMID: req.VMID, Tags: tags, Created: time.Now().UTC()}
	f.disks[id] = d
	f.record("disk.create")
	if req.VMID != "" {
		f.record("disk.attach")
	}
	cp := *d
	return &cp, nil
}

func (f *fakeAPI) AttachDisk(ctx context.Context, diskID, vmID string) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("disk.attach")
	d, ok := f.disks[diskID]
	if !ok {
		return &notFoundError{path: diskID}
	}
	d.VMID = vmID
	return nil
}

func (f *fakeAPI) DetachDisk(ctx context.Context, diskID string) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("disk.detach")
	d, ok := f.disks[diskID]
	if !ok {
		return &notFoundError{path: diskID}
	}
	d.VMID = ""
	return nil
}

func (f *fakeAPI) ResizeDisk(ctx context.Context, diskID string, sizeGiB int) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("disk.resize")
	d, ok := f.disks[diskID]
	if !ok {
		return &notFoundError{path: diskID}
	}
	d.SizeGiB = sizeGiB
	return nil
}

func (f *fakeAPI) DeleteDisk(ctx context.Context, diskID string) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("disk.delete")
	if _, ok := f.disks[diskID]; !ok {
		return &notFoundError{path: diskID}
	}
	delete(f.disks, diskID)
	return nil
}

func (f *fakeAPI) ListDisks(ctx context.Context, tags map[string]string) ([]Disk, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("disk.list")
	out := make([]Disk, 0)
	for _, d := range f.disks {
		if matchTags(d.Tags, tags) {
			cp := *d
			out = append(out, cp)
		}
	}
	return out, nil
}

func (f *fakeAPI) CreatePublicIP(ctx context.Context, req CreatePublicIPAPIRequest) (*PublicIPAddr, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.alloc("pip-")
	tags := map[string]string{}
	for k, v := range req.Tags {
		tags[k] = v
	}
	ip := &PublicIPAddr{
		ID: id, Name: req.Name,
		Address: fmt.Sprintf("52.%d.%d.%d", f.nextN%200, f.nextN%200, f.nextN%200),
		VMID: req.VMID, Tags: tags,
	}
	f.ips[id] = ip
	f.record("pip.create")
	if req.VMID != "" {
		f.record("pip.associate")
	}
	cp := *ip
	return &cp, nil
}

func (f *fakeAPI) AssociatePublicIP(ctx context.Context, ipID, vmID string) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("pip.associate")
	ip, ok := f.ips[ipID]
	if !ok {
		return &notFoundError{path: ipID}
	}
	ip.VMID = vmID
	return nil
}

func (f *fakeAPI) DisassociatePublicIP(ctx context.Context, ipID string) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("pip.disassociate")
	ip, ok := f.ips[ipID]
	if !ok {
		return nil
	}
	ip.VMID = ""
	return nil
}

func (f *fakeAPI) DeletePublicIP(ctx context.Context, ipID string) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("pip.delete")
	if _, ok := f.ips[ipID]; !ok {
		return &notFoundError{path: ipID}
	}
	delete(f.ips, ipID)
	return nil
}

func (f *fakeAPI) ListPublicIPs(ctx context.Context, tags map[string]string) ([]PublicIPAddr, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("pip.list")
	out := make([]PublicIPAddr, 0)
	for _, ip := range f.ips {
		if matchTags(ip.Tags, tags) {
			cp := *ip
			out = append(out, cp)
		}
	}
	return out, nil
}

func (f *fakeAPI) GetPricing(ctx context.Context, location, vmSize string) (float64, error) {
	_ = ctx
	_ = location
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("pricing.get")
	for _, s := range f.sizes {
		if s.Name == vmSize {
			return s.HourlyUSD, nil
		}
	}
	return 0, fmt.Errorf("unknown machine type %q", vmSize)
}

func (f *fakeAPI) vmCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.vms)
}
func (f *fakeAPI) diskCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.disks)
}
func (f *fakeAPI) ipCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.ips)
}
func (f *fakeAPI) vnetCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.vnets)
}

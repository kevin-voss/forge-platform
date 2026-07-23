package aws

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// fakeAPI is an in-memory AWS EC2 API for unit/integration tests.
type fakeAPI struct {
	mu sync.Mutex

	instances map[string]*Instance
	vpcs      map[string]*VPC
	volumes   map[string]*Volume
	eips      map[string]*ElasticIP
	regions   []RegionInfo
	types     []InstanceTypeInfo

	nextN int
	calls []string

	createDelay time.Duration
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{
		instances: map[string]*Instance{},
		vpcs:      map[string]*VPC{},
		volumes:   map[string]*Volume{},
		eips:      map[string]*ElasticIP{},
		regions:   defaultRegions(),
		types:     defaultInstanceTypes(),
		nextN:     1000,
	}
}

func (f *fakeAPI) alloc(prefix string) string {
	f.nextN++
	return fmt.Sprintf("%s%04x", prefix, f.nextN)
}

func (f *fakeAPI) record(name string) {
	f.calls = append(f.calls, name)
}

func (f *fakeAPI) CallOrder() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func matchTags(tags, filter map[string]string) bool {
	if filter == nil {
		return true
	}
	for k, v := range filter {
		if tags == nil || tags[k] != v {
			return false
		}
	}
	return true
}

func (f *fakeAPI) DescribeRegions(ctx context.Context) ([]RegionInfo, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("regions.describe")
	return append([]RegionInfo(nil), f.regions...), nil
}

func (f *fakeAPI) DescribeInstanceTypes(ctx context.Context, region string) ([]InstanceTypeInfo, error) {
	_ = ctx
	_ = region
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("instancetypes.describe")
	return append([]InstanceTypeInfo(nil), f.types...), nil
}

func (f *fakeAPI) DescribeInstances(ctx context.Context, region string, tags map[string]string) ([]Instance, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("instance.list")
	out := make([]Instance, 0)
	for _, s := range f.instances {
		if region != "" && s.Region != "" && s.Region != region {
			continue
		}
		if matchTags(s.Tags, tags) {
			cp := *s
			out = append(out, cp)
		}
	}
	return out, nil
}

func (f *fakeAPI) GetInstance(ctx context.Context, region, instanceID string) (*Instance, error) {
	_ = ctx
	_ = region
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("instance.get")
	s, ok := f.instances[instanceID]
	if !ok {
		return nil, &notFoundError{path: instanceID}
	}
	cp := *s
	return &cp, nil
}

func (f *fakeAPI) RunInstances(ctx context.Context, region string, req RunInstancesRequest) (*Instance, error) {
	_ = ctx
	if f.createDelay > 0 {
		time.Sleep(f.createDelay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// ClientToken / tag idempotency
	for _, s := range f.instances {
		if req.ClientToken != "" && s.Tags != nil && s.Tags[TagOpID] == req.ClientToken {
			f.record("instance.create")
			cp := *s
			return &cp, nil
		}
	}
	id := f.alloc("i-")
	tags := map[string]string{}
	for k, v := range req.Tags {
		tags[k] = v
	}
	inst := &Instance{
		ID:           id,
		Name:         req.Name,
		State:        "running",
		InstanceType: req.InstanceType,
		PrivateIP:    fmt.Sprintf("10.30.1.%d", f.nextN%250+2),
		PublicIP:     fmt.Sprintf("3.70.%d.%d", f.nextN%200, f.nextN%200),
		Region:       region,
		AZ:           region + "a",
		Tags:         tags,
		Created:      time.Now().UTC(),
		SubnetID:     req.SubnetID,
	}
	f.instances[id] = inst
	f.record("instance.create")
	cp := *inst
	return &cp, nil
}

func (f *fakeAPI) TerminateInstance(ctx context.Context, region, instanceID string) error {
	_ = ctx
	_ = region
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("instance.delete")
	if _, ok := f.instances[instanceID]; !ok {
		return &notFoundError{path: instanceID}
	}
	delete(f.instances, instanceID)
	return nil
}

func (f *fakeAPI) RebootInstance(ctx context.Context, region, instanceID string) error {
	_ = ctx
	_ = region
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("instance.reboot")
	if _, ok := f.instances[instanceID]; !ok {
		return &notFoundError{path: instanceID}
	}
	return nil
}

func (f *fakeAPI) CreateVPC(ctx context.Context, region string, req CreateVPCRequest) (*VPC, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.alloc("vpc-")
	subnet := f.alloc("subnet-")
	sg := f.alloc("sg-")
	tags := map[string]string{}
	for k, v := range req.Tags {
		tags[k] = v
	}
	v := &VPC{ID: id, CIDR: req.CIDR, Region: region, Tags: tags, Subnet: subnet, SG: sg}
	f.vpcs[id] = v
	f.record("vpc.create")
	cp := *v
	return &cp, nil
}

func (f *fakeAPI) DeleteVPC(ctx context.Context, region, vpcID string) error {
	_ = ctx
	_ = region
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("vpc.delete")
	if _, ok := f.vpcs[vpcID]; !ok {
		return &notFoundError{path: vpcID}
	}
	delete(f.vpcs, vpcID)
	return nil
}

func (f *fakeAPI) DescribeVPCs(ctx context.Context, region string, tags map[string]string) ([]VPC, error) {
	_ = ctx
	_ = region
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("vpc.list")
	out := make([]VPC, 0)
	for _, v := range f.vpcs {
		if matchTags(v.Tags, tags) {
			cp := *v
			out = append(out, cp)
		}
	}
	return out, nil
}

func (f *fakeAPI) CreateVolume(ctx context.Context, region string, req CreateVolumeRequest) (*Volume, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.alloc("vol-")
	tags := map[string]string{}
	for k, v := range req.Tags {
		tags[k] = v
	}
	v := &Volume{
		ID:         id,
		SizeGiB:    req.SizeGiB,
		InstanceID: req.InstanceID,
		Region:     region,
		Tags:       tags,
		Created:    time.Now().UTC(),
	}
	f.volumes[id] = v
	f.record("volume.create")
	if req.InstanceID != "" {
		f.record("volume.attach")
	}
	cp := *v
	return &cp, nil
}

func (f *fakeAPI) AttachVolume(ctx context.Context, region, volumeID, instanceID string) error {
	_ = ctx
	_ = region
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("volume.attach")
	v, ok := f.volumes[volumeID]
	if !ok {
		return &notFoundError{path: volumeID}
	}
	v.InstanceID = instanceID
	return nil
}

func (f *fakeAPI) DetachVolume(ctx context.Context, region, volumeID string) error {
	_ = ctx
	_ = region
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("volume.detach")
	v, ok := f.volumes[volumeID]
	if !ok {
		return &notFoundError{path: volumeID}
	}
	v.InstanceID = ""
	return nil
}

func (f *fakeAPI) ModifyVolume(ctx context.Context, region, volumeID string, sizeGiB int) error {
	_ = ctx
	_ = region
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("volume.resize")
	v, ok := f.volumes[volumeID]
	if !ok {
		return &notFoundError{path: volumeID}
	}
	v.SizeGiB = sizeGiB
	return nil
}

func (f *fakeAPI) DeleteVolume(ctx context.Context, region, volumeID string) error {
	_ = ctx
	_ = region
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("volume.delete")
	if _, ok := f.volumes[volumeID]; !ok {
		return &notFoundError{path: volumeID}
	}
	delete(f.volumes, volumeID)
	return nil
}

func (f *fakeAPI) DescribeVolumes(ctx context.Context, region string, tags map[string]string) ([]Volume, error) {
	_ = ctx
	_ = region
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("volume.list")
	out := make([]Volume, 0)
	for _, v := range f.volumes {
		if matchTags(v.Tags, tags) {
			cp := *v
			out = append(out, cp)
		}
	}
	return out, nil
}

func (f *fakeAPI) AllocateAddress(ctx context.Context, region string, req AllocateAddressRequest) (*ElasticIP, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.alloc("eipalloc-")
	tags := map[string]string{}
	for k, v := range req.Tags {
		tags[k] = v
	}
	ip := &ElasticIP{
		AllocationID: id,
		PublicIP:     fmt.Sprintf("18.%d.%d.%d", f.nextN%200, f.nextN%200, f.nextN%200),
		InstanceID:   req.InstanceID,
		Region:       region,
		Tags:         tags,
	}
	if req.InstanceID != "" {
		ip.AssociationID = f.alloc("eipassoc-")
	}
	f.eips[id] = ip
	f.record("eip.allocate")
	if req.InstanceID != "" {
		f.record("eip.associate")
	}
	cp := *ip
	return &cp, nil
}

func (f *fakeAPI) AssociateAddress(ctx context.Context, region, allocID, instanceID string) error {
	_ = ctx
	_ = region
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("eip.associate")
	ip, ok := f.eips[allocID]
	if !ok {
		return &notFoundError{path: allocID}
	}
	ip.InstanceID = instanceID
	ip.AssociationID = f.alloc("eipassoc-")
	return nil
}

func (f *fakeAPI) DisassociateAddress(ctx context.Context, region, assocID string) error {
	_ = ctx
	_ = region
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("eip.disassociate")
	for _, ip := range f.eips {
		if ip.AssociationID == assocID {
			ip.AssociationID = ""
			ip.InstanceID = ""
			return nil
		}
	}
	return nil
}

func (f *fakeAPI) ReleaseAddress(ctx context.Context, region, allocID string) error {
	_ = ctx
	_ = region
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("eip.release")
	if _, ok := f.eips[allocID]; !ok {
		return &notFoundError{path: allocID}
	}
	delete(f.eips, allocID)
	return nil
}

func (f *fakeAPI) DescribeAddresses(ctx context.Context, region string, tags map[string]string) ([]ElasticIP, error) {
	_ = ctx
	_ = region
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("eip.list")
	out := make([]ElasticIP, 0)
	for _, ip := range f.eips {
		if matchTags(ip.Tags, tags) {
			cp := *ip
			out = append(out, cp)
		}
	}
	return out, nil
}

func (f *fakeAPI) GetPricing(ctx context.Context, region, instanceType string) (float64, error) {
	_ = ctx
	_ = region
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("pricing.get")
	for _, t := range f.types {
		if t.ID == instanceType {
			return t.HourlyUSD, nil
		}
	}
	return 0, fmt.Errorf("unknown machine type %q", instanceType)
}

func (f *fakeAPI) instanceCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.instances)
}

func (f *fakeAPI) volumeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.volumes)
}

func (f *fakeAPI) eipCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.eips)
}

func (f *fakeAPI) vpcCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.vpcs)
}

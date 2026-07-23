package docker

import (
	"fmt"

	"forge.local/services/forge-infrastructure/internal/provider"
)

// ErrUnknownMachineType is returned when machineType is not in the local table.
var ErrUnknownMachineType = fmt.Errorf("unknown docker machine type")

// MachineTypeSpec is the compiled-in capacity for a local Docker machine type.
type MachineTypeSpec struct {
	ID        string
	CPU       int // whole cores (NanoCPUs = CPU * 1e9)
	MemoryMiB int
	Slots     int
}

var machineTypes = map[string]MachineTypeSpec{
	"docker-small":  {ID: "docker-small", CPU: 1, MemoryMiB: 1024, Slots: 2},
	"docker-medium": {ID: "docker-medium", CPU: 2, MemoryMiB: 2048, Slots: 4},
	"docker-large":  {ID: "docker-large", CPU: 4, MemoryMiB: 4096, Slots: 8},
}

// LookupMachineType returns capacity for a known docker-* type.
func LookupMachineType(id string) (MachineTypeSpec, error) {
	mt, ok := machineTypes[id]
	if !ok {
		return MachineTypeSpec{}, fmt.Errorf("%w: %q", ErrUnknownMachineType, id)
	}
	return mt, nil
}

// AllMachineTypes returns the static local table as provider.MachineType values.
func AllMachineTypes(region string) []provider.MachineType {
	order := []string{"docker-small", "docker-medium", "docker-large"}
	out := make([]provider.MachineType, 0, len(order))
	for _, id := range order {
		mt := machineTypes[id]
		out = append(out, provider.MachineType{
			ID:        mt.ID,
			Name:      mt.ID,
			CPU:       mt.CPU,
			MemoryMiB: mt.MemoryMiB,
			Region:    region,
		})
	}
	return out
}

package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"forge.local/services/forge-infrastructure/internal/bootstrap"
	"forge.local/services/forge-infrastructure/internal/bootstraptoken"
	"forge.local/services/forge-infrastructure/internal/operations"
	"forge.local/services/forge-infrastructure/internal/provider"
	"forge.local/services/forge-infrastructure/internal/provider/inventory"
	"forge.local/services/forge-infrastructure/internal/registryclient"
)

// RegistryAPI is the subset of registryclient used by the controller.
type RegistryAPI interface {
	List(ctx context.Context, plural, labelSelector string) ([]registryclient.Resource, error)
	Get(ctx context.Context, plural, name string) (*registryclient.Resource, error)
	PutStatus(ctx context.Context, plural, name, resourceVersion string, status map[string]any) (*registryclient.Resource, error)
	Create(ctx context.Context, plural string, res registryclient.Resource) (*registryclient.Resource, error)
	Delete(ctx context.Context, plural, name string) error
}

// TokenIssuer requests a single-use bootstrap token per CreateNode.
type TokenIssuer interface {
	Issue(ctx context.Context, nodePool string, ttlSeconds int64) (*bootstraptoken.Issued, error)
}

// OpLedger is the subset of operations.Ledger used by the controller.
type OpLedger interface {
	Begin(ctx context.Context, providerName, kind, targetKind, naturalKey string, request any) (*operations.BeginResult, error)
	Complete(ctx context.Context, opID string, result any, callErr error) error
}

// ProviderResolver resolves InfrastructureProvider.spec.type to a Provider.
type ProviderResolver interface {
	Resolve(typeName string, cfg map[string]any) (provider.Provider, error)
	Has(typeName string) bool
}

// NodePoolController reconciles NodePool.spec.replicas against owned Nodes.
type NodePoolController struct {
	Registry  RegistryAPI
	Ledger    OpLedger
	Providers ProviderResolver
	Nodes     *NodeController
	Tokens    TokenIssuer
	Log       *slog.Logger
	Interval  time.Duration

	ControlURL   string
	RuntimeImage string
}

// Run polls NodePools until ctx is cancelled.
func (c *NodePoolController) Run(ctx context.Context) {
	interval := c.Interval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		c.ReconcileAll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ReconcileAll lists NodePools and reconciles each.
func (c *NodePoolController) ReconcileAll(ctx context.Context) {
	pools, err := c.Registry.List(ctx, "nodepools", "")
	if err != nil {
		if c.Log != nil {
			c.Log.Warn("list nodepools failed", "error", err.Error())
		}
		return
	}
	for i := range pools {
		func(pool registryclient.Resource) {
			defer func() {
				if r := recover(); r != nil {
					if c.Log != nil {
						c.Log.Error("nodepool reconcile panic recovered",
							"nodepool", pool.Metadata.Name,
							"panic", fmt.Sprint(r),
						)
					}
				}
			}()
			if err := c.Reconcile(ctx, pool); err != nil && c.Log != nil {
				c.Log.Warn("nodepool reconcile failed",
					"nodepool", pool.Metadata.Name,
					"error", err.Error(),
				)
			}
			c.advanceOwnedNodes(ctx, pool.Metadata.Name)
		}(pools[i])
	}
}

func (c *NodePoolController) advanceOwnedNodes(ctx context.Context, poolName string) {
	if c.Nodes == nil {
		return
	}
	nodes, err := c.listOwnedNodes(ctx, poolName)
	if err != nil {
		return
	}
	for _, n := range nodes {
		if err := c.Nodes.Reconcile(ctx, n); err != nil && c.Log != nil {
			c.Log.Warn("node reconcile failed",
				"node", n.Metadata.Name,
				"error", err.Error(),
			)
		}
	}
}

// Reconcile diffs desired replicas vs ready-ish nodes and drives Create/Delete through the ledger.
func (c *NodePoolController) Reconcile(ctx context.Context, pool registryclient.Resource) error {
	name := pool.Metadata.Name
	// Node autoscaler (epic 24) writes status.desiredNodes; operator floor remains spec.replicas.
	replicas := desiredReplicasFromPool(pool)
	providerRef := stringFromSpec(pool.Spec, "providerRef")
	region := stringFromSpec(pool.Spec, "region")
	machineType := stringFromSpec(pool.Spec, "machineType")
	diskGiB := intFromSpec(pool.Spec, "diskGiB")
	publicIP := boolFromSpec(pool.Spec, "publicIP")

	nodes, err := c.listOwnedNodes(ctx, name)
	if err != nil {
		return err
	}
	ready := countReadyish(nodes)

	action := "none"
	var opID string

	// Resolve provider up front for status conditions.
	provRes, provType, cfg, resolveErr := c.resolveProvider(ctx, providerRef)
	if resolveErr != nil || provRes == nil {
		status := map[string]any{
			"phase":              "Progressing",
			"readyNodes":         ready,
			"observedGeneration": pool.Metadata.Generation,
			"conditions": []map[string]any{
				{
					"type":               "ProviderConfigured",
					"status":             "False",
					"reason":             "ProviderNotConfigured",
					"message":            errString(resolveErr),
					"lastTransitionTime": time.Now().UTC().Format(time.RFC3339),
				},
			},
		}
		status = mergePreserveAutoscalerStatus(pool.Status, status, replicas, ready)
		_, _ = c.Registry.PutStatus(ctx, "nodepools", name, pool.Metadata.ResourceVersion, status)
		if c.Log != nil {
			c.Log.Info("nodepool reconcile",
				"nodepool", name,
				"desired_replicas", replicas,
				"ready_nodes", ready,
				"action", "provider_not_configured",
				"op_id", "",
			)
		}
		return nil
	}

	maxReplicas := inventoryCeiling(provRes, cfg, provType)

	if ready < replicas {
		// Finite inventory: stop CreateNode retries once every host is claimed.
		// Do not busy-loop or fabricate capacity when desired exceeds ceiling.
		if maxReplicas >= 0 && ready >= maxReplicas {
			return c.writeInventoryExhausted(ctx, pool, ready, maxReplicas, replicas, "inventory_exhausted")
		}

		slot := nextSlot(nodes, replicas)
		naturalKey := fmt.Sprintf("%s#%d", name, slot)
		req := provider.CreateNodeRequest{
			Name:        fmt.Sprintf("%s-%d", name, slot),
			Region:      region,
			MachineType: machineType,
			DiskGiB:     diskGiB,
			PublicIP:    publicIP,
			NodePool:    name,
			Slot:        slot,
			Labels:      map[string]string{"forge.local/node-pool": name},
		}
		if err := c.attachBootstrap(ctx, &req); err != nil && c.Log != nil {
			c.Log.Warn("bootstrap token attach failed",
				"nodepool", name,
				"error", err.Error(),
			)
		}
		// Persist a redacted request so bootstrap tokens never sit unmasked in the ledger.
		ledgerReq := req
		ledgerReq.BootstrapToken = bootstrap.MaskToken(req.BootstrapToken)
		ledgerReq.UserData = "[redacted]"
		ledgerReq.Env = nil
		begin, err := c.Ledger.Begin(ctx, providerRef, operations.KindCreateNode, operations.TargetNode, naturalKey, ledgerReq)
		if err != nil {
			return err
		}
		opID = begin.Op.ID
		action = "create_node"
		if begin.SkipProvider && begin.Op.Status == operations.StatusPending {
			action = "create_node_pending"
		} else {
			// Always invoke CreateNode: adapters are idempotent by op_id, and a
			// succeeded ledger row must still recreate a deleted machine / Node.
			if begin.SkipProvider {
				action = "create_node_cached"
			}
			node, callErr := provRes.CreateNode(ctx, begin.Op.ID, req)
			if !begin.SkipProvider {
				if err := c.Ledger.Complete(ctx, begin.Op.ID, node, callErr); err != nil {
					return err
				}
			}
			if callErr != nil {
				if errors.Is(callErr, provider.ErrInventoryExhausted) {
					ceiling := maxReplicas
					if ceiling < 0 {
						ceiling = ready
					}
					return c.writeInventoryExhausted(ctx, pool, ready, ceiling, replicas, "create_node_exhausted")
				}
				if errors.Is(callErr, provider.ErrNotSupported) {
					// Unsupported mutating capability — skip, do not fail the pool.
					if c.Log != nil {
						c.Log.Info("provider capability skipped",
							"nodepool", name,
							"error", callErr.Error(),
						)
					}
				} else if errors.Is(callErr, provider.ErrProviderNotConfigured) || !c.Providers.Has(provType) {
					return c.writeProviderNotConfigured(ctx, pool, ready, callErr)
				} else if !begin.SkipProvider {
					return c.writeDegraded(ctx, pool, ready, callErr)
				}
			}
			if node != nil {
				_, _ = c.ensureNodeResource(ctx, name, req.Name, node)
			}
		}
	} else if ready > replicas {
		victim := selectScaleDownVictim(nodes, pool.Status)
		if victim != nil {
			action = "drain_node"
			if c.Nodes != nil {
				if err := c.Nodes.RequestDrain(ctx, *victim); err != nil {
					return err
				}
			} else {
				// Fallback (tests without NodeController): immediate delete via ledger.
				providerNodeID := stringFromSpec(victim.Spec, "providerNodeId")
				if providerNodeID == "" {
					providerNodeID = victim.Metadata.Name
				}
				naturalKey := victim.Metadata.Name
				begin, err := c.Ledger.Begin(ctx, providerRef, operations.KindDeleteNode, operations.TargetNode, naturalKey, map[string]any{
					"providerNodeId": providerNodeID,
				})
				if err != nil {
					return err
				}
				opID = begin.Op.ID
				action = "delete_node"
				if !begin.SkipProvider {
					callErr := provRes.DeleteNode(ctx, begin.Op.ID, providerNodeID)
					if err := c.Ledger.Complete(ctx, begin.Op.ID, nil, callErr); err != nil {
						return err
					}
					if callErr != nil {
						if errors.Is(callErr, provider.ErrProviderNotConfigured) {
							return c.writeProviderNotConfigured(ctx, pool, ready, callErr)
						}
						return c.writeDegraded(ctx, pool, ready, callErr)
					}
				}
			}
		}
	}

	phase := "Progressing"
	if ready >= replicas && replicas > 0 {
		phase = "Ready"
	}
	if ready == 0 && replicas > 0 {
		phase = "Progressing"
	}
	inventoryExhausted := maxReplicas >= 0 && replicas > maxReplicas
	if inventoryExhausted {
		phase = "Degraded"
	}

	status := map[string]any{
		"phase":              phase,
		"readyNodes":         ready,
		"observedGeneration": pool.Metadata.Generation,
		"conditions": []map[string]any{
			{
				"type":               "ProviderConfigured",
				"status":             "True",
				"reason":             "ProviderResolved",
				"lastTransitionTime": time.Now().UTC().Format(time.RFC3339),
			},
		},
	}
	if maxReplicas >= 0 {
		status["maxReplicas"] = maxReplicas
	}
	if inventoryExhausted {
		status["conditions"] = []map[string]any{
			{
				"type":               "Available",
				"status":             "False",
				"reason":             "InventoryExhausted",
				"message":            fmt.Sprintf("requested replicas=%d exceeds inventory size=%d", replicas, maxReplicas),
				"lastTransitionTime": time.Now().UTC().Format(time.RFC3339),
			},
		}
	}
	// Even when type resolves to noop fallback, surface ProviderNotConfigured.
	if !c.Providers.Has(provType) {
		status["conditions"] = []map[string]any{
			{
				"type":               "ProviderConfigured",
				"status":             "False",
				"reason":             "ProviderNotConfigured",
				"message":            fmt.Sprintf("no adapter registered for type %q", provType),
				"lastTransitionTime": time.Now().UTC().Format(time.RFC3339),
			},
		}
		status["phase"] = "Progressing"
	}
	status = mergePreserveAutoscalerStatus(pool.Status, status, replicas, ready)

	if _, err := c.Registry.PutStatus(ctx, "nodepools", name, pool.Metadata.ResourceVersion, status); err != nil {
		return err
	}
	if c.Log != nil {
		c.Log.Info("nodepool reconcile",
			"nodepool", name,
			"desired_replicas", replicas,
			"ready_nodes", ready,
			"action", action,
			"op_id", opID,
		)
	}
	return nil
}

func (c *NodePoolController) writeProviderNotConfigured(ctx context.Context, pool registryclient.Resource, ready int, err error) error {
	desired := desiredReplicasFromPool(pool)
	status := map[string]any{
		"phase":              "Progressing",
		"readyNodes":         ready,
		"observedGeneration": pool.Metadata.Generation,
		"conditions": []map[string]any{
			{
				"type":               "ProviderConfigured",
				"status":             "False",
				"reason":             "ProviderNotConfigured",
				"message":            errString(err),
				"lastTransitionTime": time.Now().UTC().Format(time.RFC3339),
			},
		},
	}
	status = mergePreserveAutoscalerStatus(pool.Status, status, desired, ready)
	_, putErr := c.Registry.PutStatus(ctx, "nodepools", pool.Metadata.Name, pool.Metadata.ResourceVersion, status)
	if c.Log != nil {
		c.Log.Info("nodepool reconcile",
			"nodepool", pool.Metadata.Name,
			"desired_replicas", desired,
			"ready_nodes", ready,
			"action", "provider_not_configured",
			"op_id", "",
		)
	}
	return putErr
}

func (c *NodePoolController) writeDegraded(ctx context.Context, pool registryclient.Resource, ready int, err error) error {
	desired := desiredReplicasFromPool(pool)
	status := map[string]any{
		"phase":              "Degraded",
		"readyNodes":         ready,
		"observedGeneration": pool.Metadata.Generation,
		"conditions": []map[string]any{
			{
				"type":               "ProviderConfigured",
				"status":             "True",
				"reason":             "ProviderCallFailed",
				"message":            errString(err),
				"lastTransitionTime": time.Now().UTC().Format(time.RFC3339),
			},
		},
	}
	status = mergePreserveAutoscalerStatus(pool.Status, status, desired, ready)
	_, putErr := c.Registry.PutStatus(ctx, "nodepools", pool.Metadata.Name, pool.Metadata.ResourceVersion, status)
	return putErr
}

func (c *NodePoolController) writeInventoryExhausted(ctx context.Context, pool registryclient.Resource, ready, maxReplicas, replicas int, action string) error {
	status := map[string]any{
		"phase":              "Degraded",
		"readyNodes":         ready,
		"maxReplicas":        maxReplicas,
		"observedGeneration": pool.Metadata.Generation,
		"conditions": []map[string]any{
			{
				"type":               "Available",
				"status":             "False",
				"reason":             "InventoryExhausted",
				"message":            fmt.Sprintf("requested replicas=%d exceeds inventory size=%d", replicas, maxReplicas),
				"lastTransitionTime": time.Now().UTC().Format(time.RFC3339),
			},
			{
				"type":               "ProviderCapacityBlocked",
				"status":             "True",
				"reason":             "ProviderCapacityBlocked",
				"message":            fmt.Sprintf("requested replicas=%d exceeds inventory size=%d", replicas, maxReplicas),
				"lastTransitionTime": time.Now().UTC().Format(time.RFC3339),
			},
		},
	}
	status = mergePreserveAutoscalerStatus(pool.Status, status, replicas, ready)
	_, putErr := c.Registry.PutStatus(ctx, "nodepools", pool.Metadata.Name, pool.Metadata.ResourceVersion, status)
	if c.Log != nil {
		c.Log.Info("nodepool reconcile",
			"nodepool", pool.Metadata.Name,
			"desired_replicas", replicas,
			"ready_nodes", ready,
			"max_replicas", maxReplicas,
			"action", action,
			"op_id", "",
		)
	}
	return putErr
}

// inventoryCeiling returns the finite capacity for ssh/bare-metal providers, or -1 if unlimited.
func inventoryCeiling(p provider.Provider, cfg map[string]any, typeName string) int {
	if cap, ok := p.(provider.InventoryCapacitor); ok {
		return cap.MaxReplicas()
	}
	switch typeName {
	case provider.TypeSSH, provider.TypeBareMetal:
		hosts, err := inventory.ParseConfig(cfg)
		if err != nil {
			return 0
		}
		return len(hosts)
	default:
		return -1
	}
}

func (c *NodePoolController) resolveProvider(ctx context.Context, providerRef string) (provider.Provider, string, map[string]any, error) {
	if providerRef == "" {
		return nil, "", nil, fmt.Errorf("%w: empty providerRef", provider.ErrProviderNotConfigured)
	}
	res, err := c.Registry.Get(ctx, "infrastructureproviders", providerRef)
	if err != nil {
		return nil, "", nil, err
	}
	if res == nil {
		return nil, "", nil, fmt.Errorf("%w: InfrastructureProvider %q not found", provider.ErrProviderNotConfigured, providerRef)
	}
	typeName := stringFromSpec(res.Spec, "type")
	cfg, _ := res.Spec["config"].(map[string]any)
	if cfg == nil {
		cfg = map[string]any{}
	} else {
		// Shallow copy so we can inject providerName without mutating the resource.
		cp := make(map[string]any, len(cfg)+4)
		for k, v := range cfg {
			cp[k] = v
		}
		cfg = cp
	}
	cfg["providerName"] = res.Metadata.Name
	if ref, ok := res.Spec["credentialsSecretRef"]; ok && ref != nil {
		cfg["credentialsSecretRef"] = ref
	}
	if region := stringFromSpec(res.Spec, "defaultRegion"); region != "" {
		cfg["defaultRegion"] = region
	}
	p, err := c.Providers.Resolve(typeName, cfg)
	if err != nil {
		return nil, typeName, cfg, err
	}
	return p, typeName, cfg, nil
}

func (c *NodePoolController) listOwnedNodes(ctx context.Context, poolName string) ([]registryclient.Resource, error) {
	// Prefer label selector; also filter by spec.nodePoolRef for robustness.
	items, err := c.Registry.List(ctx, registryclient.NodePlural, "forge.local/node-pool="+poolName)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		all, err := c.Registry.List(ctx, registryclient.NodePlural, "")
		if err != nil {
			return nil, err
		}
		for _, n := range all {
			if stringFromSpec(n.Spec, "nodePoolRef") == poolName {
				items = append(items, n)
			}
		}
	}
	out := make([]registryclient.Resource, 0, len(items))
	for _, n := range items {
		if stringFromSpec(n.Spec, "nodePoolRef") == poolName ||
			(n.Metadata.Labels != nil && n.Metadata.Labels["forge.local/node-pool"] == poolName) {
			out = append(out, n)
		}
	}
	return out, nil
}

func (c *NodePoolController) ensureNodeResource(ctx context.Context, poolName, nodeName string, pn *provider.ProviderNode) (*registryclient.Resource, error) {
	existing, err := c.Registry.Get(ctx, registryclient.NodePlural, nodeName)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if pn != nil && pn.Address != "" && stringFromStatus(existing.Status, "address") == "" {
			status := map[string]any{}
			for k, v := range existing.Status {
				status[k] = v
			}
			if status["phase"] == nil || status["phase"] == "" {
				status["phase"] = PhaseProvisioning
			}
			status["address"] = pn.Address
			if put, putErr := c.Registry.PutStatus(ctx, registryclient.NodePlural, nodeName, existing.Metadata.ResourceVersion, status); putErr == nil && put != nil {
				return put, nil
			}
		}
		return existing, nil
	}
	created, err := c.Registry.Create(ctx, registryclient.NodePlural, registryclient.Resource{
		APIVersion: "forge.dev/v1",
		Kind:       "Node",
		Metadata: registryclient.Metadata{
			Name:   nodeName,
			Labels: map[string]string{"forge.local/node-pool": poolName},
		},
		Spec: map[string]any{
			"nodePoolRef":    poolName,
			"providerNodeId": pn.ID,
			"address":        pn.Address,
		},
		Status: map[string]any{
			"phase":   PhaseProvisioning,
			"address": pn.Address,
		},
	})
	if err != nil {
		return nil, err
	}
	// Generic create ignores status — write phase/address via the status subresource.
	if created != nil {
		status := map[string]any{
			"phase":   PhaseProvisioning,
			"address": pn.Address,
		}
		if put, putErr := c.Registry.PutStatus(ctx, registryclient.NodePlural, nodeName, created.Metadata.ResourceVersion, status); putErr == nil && put != nil {
			created = put
		}
	}
	if c.Nodes != nil && c.Nodes.Timers != nil && created != nil {
		nodeID := created.Metadata.ID
		if nodeID == "" {
			nodeID = created.Metadata.Name
		}
		_ = c.Nodes.ensureTimer(ctx, nodeID, PhaseProvisioning)
	}
	return created, nil
}

func (c *NodePoolController) attachBootstrap(ctx context.Context, req *provider.CreateNodeRequest) error {
	token := ""
	if c.Tokens != nil {
		issued, err := c.Tokens.Issue(ctx, req.NodePool, 900)
		if err != nil {
			return err
		}
		if issued != nil {
			token = issued.Token
		}
	}
	controlURL := c.ControlURL
	if controlURL == "" {
		controlURL = "http://forge-control:8080"
	}
	image := c.RuntimeImage
	if image == "" {
		image = "forge/forge-runtime:local"
	}
	payload := bootstrap.Payload{
		ControlURL:     controlURL,
		BootstrapToken: token,
		NodePool:       req.NodePool,
		RuntimeImage:   image,
	}
	req.BootstrapToken = token
	req.UserData = bootstrap.RenderCloudInit(payload)
	req.Env = payload.EnvMap()
	if c.Log != nil {
		safe := payload.LogSafe()
		c.Log.Info("bootstrap payload rendered",
			"node_pool", safe["node_pool"],
			"control_url", safe["control_url"],
			"bootstrap_token", safe["bootstrap_token"],
			"runtime_image", safe["runtime_image"],
		)
	}
	return nil
}

// ResolveProviderForPool looks up the InfrastructureProvider for a NodePool.
func (c *NodePoolController) ResolveProviderForPool(ctx context.Context, poolName string) (provider.Provider, string, error) {
	pool, err := c.Registry.Get(ctx, "nodepools", poolName)
	if err != nil {
		return nil, "", err
	}
	if pool == nil {
		return nil, "", fmt.Errorf("%w: NodePool %q not found", provider.ErrProviderNotConfigured, poolName)
	}
	providerRef := stringFromSpec(pool.Spec, "providerRef")
	p, _, _, err := c.resolveProvider(ctx, providerRef)
	if err != nil {
		return nil, providerRef, err
	}
	return p, providerRef, nil
}

func countReadyish(nodes []registryclient.Resource) int {
	n := 0
	for _, node := range nodes {
		phase := stringFromStatus(node.Status, "phase")
		switch phase {
		case "Ready", "Provisioning", "Bootstrapping", "Joining":
			n++
		}
	}
	return n
}

func nextSlot(nodes []registryclient.Resource, replicas int) int {
	used := map[int]bool{}
	for _, n := range nodes {
		// Prefer trailing -<slot> in the name.
		parts := strings.Split(n.Metadata.Name, "-")
		if len(parts) > 0 {
			if slot, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
				used[slot] = true
			}
		}
	}
	for i := 0; i < replicas+len(nodes)+1; i++ {
		if !used[i] {
			return i
		}
	}
	return len(nodes)
}

func mostRecentNode(nodes []registryclient.Resource) *registryclient.Resource {
	if len(nodes) == 0 {
		return nil
	}
	sorted := make([]registryclient.Resource, len(nodes))
	copy(sorted, nodes)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Metadata.Name > sorted[j].Metadata.Name
	})
	return &sorted[0]
}

// selectScaleDownVictim prefers NodePool.status.drainCandidateNodeId / drainCandidates
// nominated by the node autoscaler (24.07); falls back to most-recent ready node.
func selectScaleDownVictim(nodes []registryclient.Resource, status map[string]any) *registryclient.Resource {
	if len(nodes) == 0 {
		return nil
	}
	preferred := drainCandidateIDs(status)
	for _, id := range preferred {
		for i := range nodes {
			n := &nodes[i]
			runtimeID := stringFromStatus(n.Status, "runtimeNodeId")
			if n.Metadata.Name == id || n.Metadata.ID == id || runtimeID == id {
				return n
			}
		}
	}
	return mostRecentNode(nodes)
}

func drainCandidateIDs(status map[string]any) []string {
	if status == nil {
		return nil
	}
	var out []string
	if id := stringFromSpec(status, "drainCandidateNodeId"); id != "" {
		out = append(out, id)
	}
	switch raw := status["drainCandidates"].(type) {
	case []any:
		for _, item := range raw {
			if s := fmt.Sprint(item); s != "" && s != "<nil>" {
				out = append(out, s)
			}
		}
	case []string:
		out = append(out, raw...)
	}
	return out
}

func stringFromSpec(spec map[string]any, key string) string {
	if spec == nil {
		return ""
	}
	v, ok := spec[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func stringFromStatus(status map[string]any, key string) string {
	return stringFromSpec(status, key)
}

func intFromSpec(spec map[string]any, key string) int {
	if spec == nil {
		return 0
	}
	v, ok := spec[key]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(t)
		return n
	default:
		return 0
	}
}

func boolFromSpec(spec map[string]any, key string) bool {
	if spec == nil {
		return false
	}
	v, ok := spec[key]
	if !ok || v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true" || t == "1"
	default:
		return false
	}
}

func errString(err error) string {
	if err == nil {
		return provider.ErrProviderNotConfigured.Error()
	}
	return err.Error()
}

// desiredReplicasFromPool prefers status.desiredNodes (node autoscaler) when higher
// than the operator floor in spec.replicas.
func desiredReplicasFromPool(pool registryclient.Resource) int {
	spec := intFromSpec(pool.Spec, "replicas")
	desired := intFromSpec(pool.Status, "desiredNodes")
	if desired > spec {
		return desired
	}
	return spec
}

var autoscalerStatusKeys = []string{
	"desiredNodes",
	"currentNodes",
	"creatingNodes",
	"failedNodes",
	"drainingNodes",
	"lastScaleUpOperationId",
	"lastScaleUpAt",
	"lastScaleDownOperationId",
	"lastScaleDownAt",
	"pendingWorkloads",
	"scaleUpRecommendation",
	"scaleDownRecommendation",
	"drainCandidates",
	"drainCandidateNodeId",
	"scaleDownPhase",
	"underutilizedNodeId",
	"underutilizedSince",
	"scaleDownDeletesInWindow",
}

var autoscalerConditionTypes = map[string]bool{
	"ScaleUpRecommended":       true,
	"ProviderCapacityBlocked":  true,
	"ScaleUpCooldown":          true,
	"NoEligibleNodePool":       true,
	"ScaleDownRecommended":     true,
	"ScaleDownBlocked":         true,
	"StatefulPrimaryProtected": true,
	"DisruptionBudgetBlocked":  true,
	"ScaleDownCanceled":        true,
	"ScaleDownCooldown":        true,
	"DeleteBlocked":            true,
}

// mergePreserveAutoscalerStatus keeps epic-24 recommendation fields when Infrastructure
// refreshes readyNodes/phase/provider conditions.
func mergePreserveAutoscalerStatus(existing, next map[string]any, desired, ready int) map[string]any {
	if next == nil {
		next = map[string]any{}
	}
	for _, key := range autoscalerStatusKeys {
		if _, set := next[key]; set {
			continue
		}
		if existing != nil {
			if v, ok := existing[key]; ok {
				next[key] = v
			}
		}
	}
	if _, ok := next["desiredNodes"]; !ok && desired > 0 {
		next["desiredNodes"] = desired
	}
	if _, ok := next["currentNodes"]; !ok {
		next["currentNodes"] = ready
	}
	if _, set := next["creatingNodes"]; !set {
		dn := intFromSpec(next, "desiredNodes")
		c := dn - ready
		if c < 0 {
			c = 0
		}
		next["creatingNodes"] = c
	}

	// Merge conditions: keep autoscaler-owned types from existing unless next sets them.
	existingConds := conditionsFromStatus(existing)
	nextConds := conditionsFromStatus(next)
	byType := map[string]map[string]any{}
	for _, c := range existingConds {
		t := stringFromSpec(c, "type")
		if autoscalerConditionTypes[t] {
			byType[t] = c
		}
	}
	for _, c := range nextConds {
		t := stringFromSpec(c, "type")
		byType[t] = c
	}
	merged := make([]map[string]any, 0, len(byType))
	for _, c := range byType {
		merged = append(merged, c)
	}
	sort.Slice(merged, func(i, j int) bool {
		return stringFromSpec(merged[i], "type") < stringFromSpec(merged[j], "type")
	})
	if len(merged) > 0 {
		next["conditions"] = merged
	}
	return next
}

func conditionsFromStatus(status map[string]any) []map[string]any {
	if status == nil {
		return nil
	}
	raw, ok := status["conditions"].([]any)
	if !ok {
		// Also accept []map[string]any written by this controller.
		if typed, ok := status["conditions"].([]map[string]any); ok {
			return typed
		}
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

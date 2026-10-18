package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
)

// InstanceStatus says whether an instance can be used.
type InstanceStatus string

// The statuses an instance can resolve to.
const (
	StatusReady           InstanceStatus = "ready"
	StatusDisabled        InstanceStatus = "disabled"
	StatusUnknownProvider InstanceStatus = "unknown-provider"
	StatusInvalidConfig   InstanceStatus = "invalid-config"
)

// ResolvedInstance is an instance plus what the runtime made of it.
type ResolvedInstance struct {
	Instance Instance
	// Definition is nil when the provider id is not registered.
	Definition *Definition
	Status     InstanceStatus
	// Error says why it is not ready. Redacted.
	Error string
}

// InstanceClient is a ready instance with its client.
type InstanceClient struct {
	Instance Instance
	Client   any
}

// Runtime turns the instances of a ConfigSource into clients. It is safe for
// concurrent use.
type Runtime struct {
	registry *Registry
	source   ConfigSource

	mu      sync.Mutex
	clients map[string]cachedClient // by instance id
}

type cachedClient struct {
	configKey [sha256.Size]byte
	client    any
}

// NewRuntime builds a runtime over a registry and a config source.
func NewRuntime(registry *Registry, source ConfigSource) *Runtime {
	return &Runtime{registry: registry, source: source, clients: map[string]cachedClient{}}
}

type resolution struct {
	ResolvedInstance
	config any // the validated config, when ready
}

func (r *Runtime) resolveAll(ctx context.Context) ([]resolution, error) {
	instances, err := r.source.Instances(ctx)
	if err != nil {
		return nil, &ProviderError{Message: "could not read the instance configuration"}
	}
	out := make([]resolution, 0, len(instances))
	for _, inst := range instances {
		if !inst.Enabled {
			out = append(out, resolution{ResolvedInstance: ResolvedInstance{Instance: inst, Status: StatusDisabled}})
			continue
		}
		def, ok := r.registry.Get(inst.ProviderID)
		if !ok {
			out = append(out, resolution{ResolvedInstance: ResolvedInstance{
				Instance: inst,
				Status:   StatusUnknownProvider,
				Error:    Redact(fmt.Sprintf("Unknown provider %q", inst.ProviderID)),
			}})
			continue
		}
		cfg, err := def.Decode(inst.Config)
		if err != nil {
			out = append(out, resolution{ResolvedInstance: ResolvedInstance{
				Instance:   inst,
				Definition: def,
				Status:     StatusInvalidConfig,
				Error:      Redact(err.Error(), def.spec.secretValues(inst.Config)...),
			}})
			continue
		}
		out = append(out, resolution{
			ResolvedInstance: ResolvedInstance{Instance: inst, Definition: def, Status: StatusReady},
			config:           cfg,
		})
	}
	r.forget(out)
	return out, nil
}

// forget drops clients of instances that no longer exist. (The TypeScript
// version keeps them forever; harmless there, a slow leak with a database source.)
func (r *Runtime) forget(current []resolution) {
	live := make(map[string]bool, len(current))
	for _, c := range current {
		live[c.Instance.ID] = true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id := range r.clients {
		if !live[id] {
			delete(r.clients, id)
		}
	}
}

// clientFor reuses a client while the instance's config is unchanged. The cache
// key covers provider, name and validated config. (The TypeScript key covered
// only the config, so renaming an instance kept a client holding the old name.)
func (r *Runtime) clientFor(res resolution) any {
	inst := res.Instance
	encoded, _ := json.Marshal([]any{inst.ProviderID, inst.Name, res.config})
	key := sha256.Sum256(encoded)

	r.mu.Lock()
	defer r.mu.Unlock()
	if cached, ok := r.clients[inst.ID]; ok && cached.configKey == key {
		return cached.client
	}
	client := res.Definition.Create(res.config, ProviderContext{InstanceID: inst.ID, InstanceName: inst.Name})
	r.clients[inst.ID] = cachedClient{configKey: key, client: client}
	return client
}

// ListInstances returns every configured instance with its status, including
// broken ones, so the UI can say why.
func (r *Runtime) ListInstances(ctx context.Context) ([]ResolvedInstance, error) {
	all, err := r.resolveAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ResolvedInstance, len(all))
	for i, res := range all {
		out[i] = res.ResolvedInstance
	}
	return out, nil
}

// Clients returns the ready instances of a role, each with its client.
func (r *Runtime) Clients(ctx context.Context, role Role) ([]InstanceClient, error) {
	all, err := r.resolveAll(ctx)
	if err != nil {
		return nil, err
	}
	var out []InstanceClient
	for _, res := range all {
		if res.Status != StatusReady || res.Definition.Role != role {
			continue
		}
		out = append(out, InstanceClient{Instance: res.Instance, Client: r.clientFor(res)})
	}
	return out, nil
}

// Client returns one instance's client, or a *ProviderError if it is not ready.
func (r *Runtime) Client(ctx context.Context, instanceID string) (any, error) {
	all, err := r.resolveAll(ctx)
	if err != nil {
		return nil, err
	}
	for _, res := range all {
		if res.Instance.ID != instanceID {
			continue
		}
		if res.Status != StatusReady {
			msg := fmt.Sprintf("Instance %q is %s", instanceID, res.Status)
			if res.Error != "" {
				msg += ": " + res.Error
			}
			return nil, &ProviderError{Message: Redact(msg)}
		}
		return r.clientFor(res), nil
	}
	return nil, &ProviderError{Message: Redact(fmt.Sprintf("No instance %q is configured", instanceID))}
}

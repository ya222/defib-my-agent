package provider

import (
	"fmt"
	"sort"
	"sync"
)

// Registry holds a set of Providers keyed by their Name.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

// Register adds p to the registry. It is an error to register two providers with the same
// Name.
func (r *Registry) Register(p Provider) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := p.Name()
	if _, exists := r.providers[name]; exists {
		return fmt.Errorf("register provider: %q is already registered", name)
	}
	r.providers[name] = p
	return nil
}

// Get returns the provider registered under name, or an error naming the requested provider
// and listing the known ones if none matches.
func (r *Registry) Get(name string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[name]
	if !ok {
		known := make([]string, 0, len(r.providers))
		for n := range r.providers {
			known = append(known, n)
		}
		sort.Strings(known)
		return nil, fmt.Errorf("get provider %q: not registered (known providers: %v)", name, known)
	}
	return p, nil
}

// List returns all registered providers sorted by Name.
func (r *Registry) List() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Default is the process-wide registry used by the daemon.
var Default = NewRegistry()

// Register adds p to the Default registry.
func Register(p Provider) error { return Default.Register(p) }

// Get returns the provider registered under name in the Default registry.
func Get(name string) (Provider, error) { return Default.Get(name) }

// List returns all providers registered in the Default registry, sorted by Name.
func List() []Provider { return Default.List() }

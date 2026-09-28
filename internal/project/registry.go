package project

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"facets.barnlab.dev/internal/nilcheck"
)

// Registry stores providers by their stable name.
type Registry struct {
	providers map[string]Provider
	mu        sync.RWMutex
}

// NewRegistry returns an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

// Register adds provider. Provider names are case-sensitive and must not be empty.
func (r *Registry) Register(provider Provider) error {
	if nilcheck.IsNil(provider) {
		return errors.New("project: provider is required")
	}
	name := provider.Name()
	if name == "" {
		return errors.New("project: provider name is required")
	}
	if strings.TrimSpace(name) != name {
		return errors.New("project: provider name must not contain surrounding whitespace")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.providers[name]; exists {
		return fmt.Errorf("%w: %s", ErrProviderExists, name)
	}
	r.providers[name] = provider
	return nil
}

// Provider returns the registered provider with name.
func (r *Registry) Provider(name string) (Provider, error) {
	r.mu.RLock()
	provider, exists := r.providers[name]
	r.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, name)
	}
	return provider, nil
}

// Names returns registered provider names in stable order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	r.mu.RUnlock()
	sort.Strings(names)
	return names
}

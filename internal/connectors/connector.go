package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/HarshShah0203/homedex/internal/domain"
)

type Config map[string]json.RawMessage

// DecodeConfig converts the connector framework's raw JSON config into a
// connector-specific typed config while preserving JSON decoding errors.
func DecodeConfig[T any](cfg Config) (T, error) {
	var decoded T
	b, err := json.Marshal(cfg)
	if err != nil {
		return decoded, err
	}
	if err = json.Unmarshal(b, &decoded); err != nil {
		return decoded, err
	}
	return decoded, nil
}

type Connector interface {
	Kind() string
	Validate(context.Context, Config) error
	Scan(context.Context, Config) (domain.Snapshot, error)
}

type Registry struct {
	mu         sync.RWMutex
	connectors map[string]Connector
}

func NewRegistry() *Registry { return &Registry{connectors: make(map[string]Connector)} }

func (r *Registry) Register(c Connector) error {
	if c == nil || c.Kind() == "" {
		return fmt.Errorf("connector kind is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.connectors[c.Kind()]; exists {
		return fmt.Errorf("connector %q already registered", c.Kind())
	}
	r.connectors[c.Kind()] = c
	return nil
}

func (r *Registry) Get(kind string) (Connector, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.connectors[kind]
	return c, ok
}

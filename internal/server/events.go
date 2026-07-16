package server

import (
	"encoding/json"
	"sync"

	"github.com/HarshShah0203/homedex/internal/engine"
)

type Broker struct {
	mu      sync.RWMutex
	clients map[chan []byte]struct{}
}

func NewBroker() *Broker { return &Broker{clients: make(map[chan []byte]struct{})} }
func (b *Broker) Subscribe() (chan []byte, func()) {
	ch := make(chan []byte, 16)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if _, ok := b.clients[ch]; ok {
			delete(b.clients, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
}
func (b *Broker) Publish(event engine.Event) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.clients {
		select {
		case ch <- data:
		default:
		}
	}
}

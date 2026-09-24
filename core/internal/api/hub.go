package api

import (
	"encoding/json"
	"sync"
)

// Hub fans server events (status changes, alerts, exec progress) out to every
// connected UI window.
type Hub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[chan []byte]struct{}{}} }

func (h *Hub) Publish(kind string, payload any) {
	msg, err := json.Marshal(struct {
		Type string `json:"type"`
		Data any    `json:"data"`
	}{kind, payload})
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- msg:
		default: // a stuck client must not block monitoring; it resyncs via REST
		}
	}
}

func (h *Hub) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 256)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

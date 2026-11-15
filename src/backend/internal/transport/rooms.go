package transport

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/isaru66/go-realtime-quiz/backend/internal/game"
	repository "github.com/isaru66/go-realtime-quiz/backend/internal/redis"
)

type roomUpdate struct {
	state    *game.State
	err      error
	sequence uint64
}
type room struct {
	mu       sync.Mutex
	watchers map[chan roomUpdate]struct{}
	cancel   context.CancelFunc
	sequence atomic.Uint64
}
type rooms struct {
	mu    sync.Mutex
	items map[string]*room
}

func (s *Server) watch(pin, id string) (<-chan roomUpdate, func(), func() uint64) {
	key := pin + ":" + id
	s.rooms.mu.Lock()
	r := s.rooms.items[key]
	if r == nil {
		ctx, cancel := context.WithCancel(s.Context)
		r = &room{watchers: map[chan roomUpdate]struct{}{}, cancel: cancel}
		s.rooms.items[key] = r
		go s.runRoom(ctx, r, pin, id)
	}
	channel := make(chan roomUpdate, 1)
	r.mu.Lock()
	r.watchers[channel] = struct{}{}
	r.mu.Unlock()
	s.rooms.mu.Unlock()
	return channel, func() {
		s.rooms.mu.Lock()
		defer s.rooms.mu.Unlock()
		r.mu.Lock()
		delete(r.watchers, channel)
		empty := len(r.watchers) == 0
		r.mu.Unlock()
		if empty {
			r.cancel()
			delete(s.rooms.items, key)
		}
	}, r.sequence.Load
}
func (r *room) broadcast(update roomUpdate) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for ch := range r.watchers {
		select {
		case ch <- update:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- update:
			default:
			}
		}
	}
}
func (s *Server) runRoom(ctx context.Context, r *room, pin, id string) {
	sub := s.Store.Client.Subscribe(ctx, repository.Channel(pin, id))
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		r.broadcast(roomUpdate{err: err})
		return
	}
	messages := sub.Channel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	dirty := true
	last := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-messages:
			s.messages.WithLabelValues("notification").Inc()
			dirty = true
		case <-ticker.C:
			if !dirty && time.Since(last) < time.Second {
				continue
			}
			s.messages.WithLabelValues("reconciliation").Inc()
			sequence := r.sequence.Add(1)
			state, err := s.Store.Read(ctx, pin, id, "", "")
			r.broadcast(roomUpdate{state: state, err: err, sequence: sequence})
			last = time.Now()
			dirty = false
		}
	}
}

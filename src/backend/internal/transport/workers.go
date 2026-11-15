package transport

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/isaru66/go-realtime-quiz/backend/internal/game"
	repository "github.com/isaru66/go-realtime-quiz/backend/internal/redis"
)

func (s *Server) Workers(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			for partition := 0; partition < 64; partition++ {
				if ctx.Err() != nil {
					return
				}
				members, err := s.Store.Due(ctx, partition)
				if err != nil {
					s.Log.Warn("deadline discovery unavailable", "partition", partition)
					break
				}
				for _, member := range members {
					pin, id, ok := strings.Cut(member, ":")
					if !ok {
						continue
					}
					if err = s.Engine.Step(ctx, pin, id); err != nil {
						var e *game.Error
						if errors.As(err, &e) && e.Code == "session_unavailable" {
							if err = s.Store.RemoveDue(ctx, pin, id); err != nil {
								s.Log.Warn("deadline cleanup failed", "game_pin", pin, "session_id", id)
							}
						} else {
							s.Log.Warn("deadline processing failed", "game_pin", pin, "session_id", id, "error", err)
						}
					}
				}
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			previous, status, _, _ := s.Catalog.Get()
			err := s.Catalog.Reload(s.Options.QuizPath, game.Time(time.Now().UnixMilli()))
			current, next, _, _ := s.Catalog.Get()
			if err != nil && status != next {
				s.Log.Error("configuration reload rejected", "error", err)
			}
			if err == nil && (previous == nil || previous.Version != current.Version) {
				s.Log.Info("configuration reloaded", "version", current.Version)
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			games, players := 0, 0
			complete := true
			for partition := 0; partition < 64; partition++ {
				// Scan bounded pages, never fetch an unbounded cluster-wide game list.
				var cursor uint64
				for {
					page, next, err := s.Store.Client.ZScan(ctx, repository.Partition(partition)+"active", cursor, "*", 100).Result()
					if err != nil {
						complete = false
						break
					}
					for i := 0; i < len(page); i += 2 {
						pin, id, ok := strings.Cut(page[i], ":")
						if !ok {
							continue
						}
						state, err := s.Store.Read(ctx, pin, id, "", "")
						if err != nil {
							complete = false
							continue
						}
						if state.Now < state.Meta.Expires && state.Meta.State != game.Expired {
							games++
							for _, p := range state.Players {
								if !p.Left {
									players++
								}
							}
						}
					}
					cursor = next
					if cursor == 0 {
						break
					}
				}
				if !complete {
					break
				}
			}
			if complete {
				s.activeGames.Set(float64(games))
				s.activePlayers.Set(float64(players))
			} else {
				s.Log.Warn("shared activity metrics sample unavailable")
			}
		}
	}()
}

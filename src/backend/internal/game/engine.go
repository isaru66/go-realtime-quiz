package game

import (
	"context"
	"errors"
	"strconv"
	"time"
)

type Mutation struct {
	Meta         Meta    `json:"meta"`
	Kind         string  `json:"kind"`
	Touch        bool    `json:"touch"`
	Finished     bool    `json:"finished"`
	Duration     int64   `json:"duration"`
	Publish      bool    `json:"publish"`
	Member       *Member `json:"member,omitempty"`
	Player       string  `json:"player"`
	CommandField string  `json:"command_field"`
	CommandValue string  `json:"command_value"`
}
type Repository interface {
	Read(context.Context, string, string, string, string) (*State, error)
	Commit(context.Context, *State, Mutation) error
}
type Engine struct{ Repo Repository }

func retry(ctx context.Context, attempt int) error {
	timer := time.NewTimer(time.Duration(1+attempt) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func conflict(err error) bool { var e *Error; return errors.As(err, &e) && e.Code == "retry" }

func (e *Engine) Bind(ctx context.Context, pin, id, player, name, digest, connection string, joining bool) (*State, error) {
	for attempt := 0; attempt < 64; attempt++ {
		s, err := e.Repo.Read(ctx, pin, id, player, "")
		if err != nil {
			return nil, err
		}
		member, exists := s.Players[player]
		if exists {
			_, err = s.Authenticate(player, digest, "")
			if err != nil {
				return nil, err
			}
			if member.Connection == connection {
				return s, nil
			}
		} else {
			if !joining {
				return nil, Fail("invalid_credentials")
			}
			if s.Now >= s.Meta.Expires {
				return nil, Fail("wrong_pin")
			}
			if s.Meta.State != Lobby {
				return nil, Fail("game_already_started")
			}
			if len(s.Players) >= 1000 {
				return nil, Fail("capacity_exceeded")
			}
			member = Member{ID: player, Name: name, Digest: digest}
		}
		member.Generation++
		member.Connection = connection
		member.Presence = s.Now + 45000
		m := Mutation{Meta: s.Meta, Kind: "bind", Member: &member, Player: player}
		if !exists {
			m.Kind = "join"
			m.Touch = true
		}
		if err = e.Repo.Commit(ctx, s, m); err == nil {
			return e.Repo.Read(ctx, pin, id, player, "")
		} else if !conflict(err) {
			return nil, err
		}
		if err = retry(ctx, attempt); err != nil {
			return nil, err
		}
	}
	return nil, Fail("temporarily_unavailable")
}

func (e *Engine) Control(ctx context.Context, pin, id, capability, kind, command string, expected int64) (*State, error) {
	fingerprint := Digest(kind + ":" + strconv.FormatInt(expected, 10))
	for attempt := 0; attempt < 32; attempt++ {
		s, err := e.Repo.Read(ctx, pin, id, "host", command)
		if err != nil {
			return nil, err
		}
		if !EqualDigest(s.Meta.HostDigest, Digest(capability)) {
			return nil, Fail("forbidden")
		}
		if s.Now >= s.Meta.Expires {
			return nil, Fail("session_expired")
		}
		if s.Receipt != "" {
			if s.Receipt != fingerprint {
				return nil, Fail("idempotency_conflict")
			}
			return s, nil
		}
		if s.CommandCount >= 20000 {
			return nil, Fail("capacity_exceeded")
		}
		if expected != s.Meta.Control {
			return nil, Fail("stale_control_revision")
		}
		m := Mutation{Meta: s.Meta, Kind: kind, Touch: true, CommandField: "host:" + command, CommandValue: fingerprint}
		m.Meta.Control++
		switch kind {
		case "start_game":
			if s.Meta.State != Lobby {
				return nil, Fail("invalid_state")
			}
			m.Meta.Index = 0
			m.Meta.State = Open
			m.Duration = s.Quiz.Questions[0].Duration * 1000
		case "next_question":
			if s.Meta.State != Closed {
				return nil, Fail("invalid_state")
			}
			if s.Meta.Index+1 == len(s.Quiz.Questions) {
				m.Meta.State = Finished
				m.Finished = true
			} else {
				m.Meta.Index++
				m.Meta.State = Open
				m.Duration = s.Quiz.Questions[m.Meta.Index].Duration * 1000
			}
		case "end_game":
			if s.Meta.State == Finished || s.Meta.State == Expired {
				return nil, Fail("invalid_state")
			}
			m.Meta.State = Finished
			m.Finished = true
			m.Publish = s.Meta.State == Open
		default:
			return nil, Fail("invalid_message")
		}
		if err = e.Repo.Commit(ctx, s, m); err == nil {
			return e.Repo.Read(ctx, pin, id, "host", "")
		} else if !conflict(err) {
			return nil, err
		}
		if err = retry(ctx, attempt); err != nil {
			return nil, err
		}
	}
	return nil, Fail("temporarily_unavailable")
}

func (e *Engine) Leave(ctx context.Context, pin, id, player, digest, connection, command string) error {
	for attempt := 0; attempt < 32; attempt++ {
		s, err := e.Repo.Read(ctx, pin, id, player, command)
		if err != nil {
			return err
		}
		member, err := s.Authenticate(player, digest, connection)
		if err != nil {
			if ge := new(Error); errors.As(err, &ge) && ge.Code == "membership_left" && s.Receipt == "leave" {
				return nil
			}
			return err
		}
		if s.CommandCount >= 20000 {
			return Fail("capacity_exceeded")
		}
		member.Left = true
		member.Presence = 0
		m := Mutation{Meta: s.Meta, Kind: "leave", Member: &member, CommandField: player + ":" + command, CommandValue: "leave"}
		if err = e.Repo.Commit(ctx, s, m); err == nil {
			return nil
		} else if !conflict(err) {
			return err
		}
		if err = retry(ctx, attempt); err != nil {
			return err
		}
	}
	return Fail("temporarily_unavailable")
}
func (e *Engine) Step(ctx context.Context, pin, id string) error {
	for attempt := 0; attempt < 8; attempt++ {
		s, err := e.Repo.Read(ctx, pin, id, "", "")
		if err != nil {
			return err
		}
		m := Mutation{Meta: s.Meta}
		if s.Meta.State == Expired {
			return nil
		}
		if s.Now >= s.Meta.Expires {
			m.Kind = "expire"
			m.Meta.State = Expired
			m.Meta.Control++
		} else if s.Meta.State == Open && s.Now >= s.Meta.Deadline {
			m.Kind = "close"
			m.Meta.State = Closed
			m.Meta.Control++
			m.Publish = true
		} else {
			return nil
		}
		if err = e.Repo.Commit(ctx, s, m); err == nil {
			return nil
		} else if !conflict(err) {
			return err
		}
	}
	return Fail("temporarily_unavailable")
}

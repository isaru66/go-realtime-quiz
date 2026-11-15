package transport

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gofiber/contrib/websocket"
	"github.com/google/uuid"
	"github.com/isaru66/go-realtime-quiz/backend/internal/game"
)

type command struct {
	ID        string          `json:"event_id"`
	Type      string          `json:"event_type"`
	Version   int             `json:"protocol_version"`
	PIN       string          `json:"game_pin"`
	Session   string          `json:"session_id"`
	Player    string          `json:"player_id"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}
type event struct {
	ID        string `json:"event_id"`
	Type      string `json:"event_type"`
	Version   int    `json:"protocol_version"`
	PIN       string `json:"game_pin"`
	Session   string `json:"session_id,omitempty"`
	Player    string `json:"player_id,omitempty"`
	Timestamp string `json:"timestamp"`
	Revision  int64  `json:"revision,omitempty"`
	Reply     string `json:"reply_to,omitempty"`
	Payload   any    `json:"payload"`
}
type binding struct {
	pin, id, player, digest, capability, admin, connection string
	role                                                   string
}
type incoming struct {
	message []byte
	err     error
	kind    int
}

func (s *Server) decode(data []byte) (command, error) {
	var cmd command
	var raw any
	if err := strict(data, &raw); err != nil {
		return cmd, err
	}
	if err := s.Schema.Validate(raw); err != nil {
		return cmd, err
	}
	if err := json.Unmarshal(data, &cmd); err != nil {
		return cmd, err
	}
	return cmd, nil
}
func (s *Server) socket(c *websocket.Conn) {
	if s.socketCount.Add(1) > 4096 {
		s.socketCount.Add(-1)
		_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1013, "capacity"), time.Now().Add(time.Second))
		_ = c.Close()
		return
	}
	defer s.socketCount.Add(-1)
	s.connections.Inc()
	defer s.connections.Dec()
	defer c.Close()
	c.SetReadLimit(64 * 1024)
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	ctx, cancel := context.WithCancel(s.Context)
	defer cancel()
	input := make(chan incoming, 4)
	pongs := make(chan struct{}, 1)
	c.SetPongHandler(func(string) error {
		_ = c.SetReadDeadline(time.Now().Add(45 * time.Second))
		select {
		case pongs <- struct{}{}:
		default:
		}
		return nil
	})
	go func() {
		for {
			kind, data, err := c.ReadMessage()
			select {
			case input <- incoming{data, err, kind}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	b := binding{connection: uuid.NewString()}
	var revision int64
	send := func(kind, reply string, payload any, rev int64) bool {
		if payload == nil {
			payload = map[string]any{}
		}
		_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return c.WriteJSON(event{uuid.NewString(), kind, 1, b.pin, b.id, b.player, game.Time(time.Now().UnixMilli()), rev, reply, payload}) == nil
	}
	closeSocket := func(code int) {
		_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, ""), time.Now().Add(time.Second))
	}
	sendError := func(cmd command, err error) {
		kind := "error"
		if cmd.Type == "reconnect" {
			kind = "reconnect_failed"
		}
		body := errorBody(err)
		s.rejections.WithLabelValues(body.Code).Inc()
		if body.Code == "temporarily_unavailable" {
			s.Log.Error("WebSocket dependency failure", "game_pin", b.pin, "session_id", b.id, "error", err)
		}
		send(kind, cmd.ID, body, revision)
	}
	var first command
	bindTimer := time.NewTimer(10 * time.Second)
	defer bindTimer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-bindTimer.C:
		closeSocket(1008)
		return
	case item := <-input:
		if item.err != nil {
			return
		}
		if item.kind != websocket.TextMessage {
			closeSocket(1003)
			return
		}
		var err error
		first, err = s.decode(item.message)
		if err != nil {
			closeSocket(1008)
			return
		}
	}
	b.pin, b.id, b.player = first.PIN, first.Session, first.Player
	var state *game.State
	var err error
	if first.Type == "join_game" || first.Type == "reconnect" {
		if err = s.Store.Limit(ctx, "join", stringLocal(c, "client_ip"), s.JoinRate); err == nil {
			var payload struct {
				Name  string `json:"player_name"`
				Token string `json:"reconnect_token"`
				Last  int64  `json:"last_revision"`
			}
			err = json.Unmarshal(first.Payload, &payload)
			if err == nil && (!validToken(payload.Token) || (first.Type == "join_game" && !validName(payload.Name))) {
				err = game.Fail("invalid_message")
			}
			if err == nil {
				b.digest = game.Digest(payload.Token)
				if b.id == "" {
					b.id, err = s.Store.Lookup(ctx, b.pin)
				} else if first.Type == "join_game" {
					var current string
					current, err = s.Store.Lookup(ctx, b.pin)
					if err == nil && current != b.id {
						err = game.Fail("session_mismatch")
					}
				}
				if err == nil {
					state, err = s.Engine.Bind(ctx, b.pin, b.id, b.player, payload.Name, b.digest, b.connection, first.Type == "join_game")
				}
			}
		}
		b.role = "player"
		if first.Type == "reconnect" {
			result := "success"
			if err != nil {
				result = "failure"
			}
			s.reconnects.WithLabelValues(result).Inc()
		}
	} else if first.Type == "host_connect" {
		var payload struct {
			Capability string `json:"host_capability"`
			CSRF       string `json:"csrf_token"`
		}
		err = json.Unmarshal(first.Payload, &payload)
		b.admin = stringLocal(c, "admin_token")
		b.capability = payload.Capability
		b.role = "host"
		b.player = ""
		if err == nil {
			err = s.admin(b.admin, payload.CSRF, true)
		}
		if err == nil {
			state, err = s.Store.Read(ctx, b.pin, b.id, "", "")
		}
		if err == nil && !game.EqualDigest(state.Meta.HostDigest, game.Digest(b.capability)) {
			err = game.Fail("forbidden")
		}
		if err == nil && state.Now >= state.Meta.Expires {
			err = game.Fail("session_expired")
		}
	} else {
		err = game.Fail("unauthenticated")
	}
	if err != nil {
		var ge *game.Error
		if errors.As(err, &ge) && ge.Code == "membership_left" {
			if previous, e := s.Store.Read(ctx, b.pin, b.id, b.player, ""); e == nil {
				send("game_left", first.ID, nil, previous.Meta.Revision)
				closeSocket(1000)
				return
			}
		}
		sendError(first, err)
		if errorBody(err).Retryable {
			closeSocket(1013)
		} else {
			closeSocket(1008)
		}
		return
	}
	updates, unwatch, readerSequence := s.watch(b.pin, b.id)
	defer unwatch()
	state, err = s.Store.Read(ctx, b.pin, b.id, b.player, "")
	if err != nil {
		sendError(first, err)
		closeSocket(1013)
		return
	}
	revision = state.Meta.Revision
	// Ignore room reads begun before this socket's initial snapshot completed.
	catchupBarrier := readerSequence()
	replyType := "game_joined"
	if b.role == "host" {
		replyType = "host_connected"
	} else if first.Type == "reconnect" {
		replyType = "reconnect_success"
	}
	if !send(replyType, first.ID, state.Snapshot(b.role, b.player), revision) {
		return
	}
	_ = c.SetReadDeadline(time.Now().Add(45 * time.Second))
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	lastSnapshot := time.Now()
	lastControl := state.Meta.Control
	lastMembers := len(state.Players)
	previousPlayers := state.Players
	var lastAnswer string
	if a, ok := state.Answers[b.player]; ok {
		lastAnswer = a.Command
	}
	for {
		select {
		case <-ctx.Done():
			send("server_draining", "", nil, revision)
			closeSocket(1012)
			return
		case <-ping.C:
			if err = c.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); err != nil {
				return
			}
		case <-pongs:
			if b.role == "player" {
				var ok bool
				ok, err = s.Store.Heartbeat(ctx, b.pin, b.id, b.player, b.connection)
				if err != nil {
					closeSocket(1013)
					return
				}
				if !ok {
					send("error", "", errorBody(game.Fail("connection_replaced")), revision)
					closeSocket(1008)
					return
				}
			} else if err = s.admin(b.admin, "", false); err != nil {
				send("error", "", errorBody(err), revision)
				closeSocket(1008)
				return
			}
		case update := <-updates:
			if update.err != nil {
				send("error", "", errorBody(update.err), revision)
				closeSocket(1013)
				return
			}
			if update.sequence <= catchupBarrier {
				continue
			}
			next := update.state
			if next.Meta.Revision < revision {
				closeSocket(1012)
				return
			}
			if b.role == "player" {
				_, err = next.Authenticate(b.player, b.digest, b.connection)
				if err != nil {
					if e := new(game.Error); errors.As(err, &e) && e.Code == "session_expired" {
						send("error", "", errorBody(err), next.Meta.Revision)
					} else {
						send("error", "", errorBody(err), next.Meta.Revision)
					}
					closeSocket(1008)
					return
				}
			}
			ownCommand := ""
			if a, ok := next.Answers[b.player]; ok {
				ownCommand = a.Command
			}
			rosterChanged := len(next.Players) != lastMembers
			for id, member := range next.Players {
				old, exists := previousPlayers[id]
				if !exists || old.Left != member.Left {
					rosterChanged = true
				}
			}
			changed := next.Meta.Control != lastControl || rosterChanged || ownCommand != lastAnswer || next.Meta.State == game.Expired
			if !changed && time.Since(lastSnapshot) < 5*time.Second {
				continue
			}
			view := next.Snapshot(b.role, b.player)
			for id, member := range next.Players {
				old, exists := previousPlayers[id]
				public := game.PublicMember{ID: id, Name: member.Name, Connected: !member.Left && member.Presence > next.Now, Left: member.Left}
				if !exists {
					send("player_joined", "", public, next.Meta.Revision)
				} else if !old.Left && member.Left {
					send("player_left", "", public, next.Meta.Revision)
				}
			}
			if next.Meta.Control != lastControl {
				if next.Meta.State == game.Open && view.Question != nil {
					if lastControl == 0 {
						send("game_started", "", nil, next.Meta.Revision)
					}
					send("question_started", "", view.Question, next.Meta.Revision)
				}
				if (next.Meta.State == game.Closed || next.Meta.State == game.Finished) && view.ClosedResult != nil {
					send("question_ended", "", view.ClosedResult, next.Meta.Revision)
					if view.OwnResult != nil {
						send("answer_result", "", view.OwnResult, next.Meta.Revision)
					}
					if view.Statistics != nil {
						send("answer_statistics", "", view.Statistics, next.Meta.Revision)
					}
					send("leaderboard_updated", "", map[string]any{"leaderboard": view.Leaderboard}, next.Meta.Revision)
				}
				if next.Meta.State == game.Finished {
					send("game_finished", "", nil, next.Meta.Revision)
				}
			}
			if !send("state_snapshot", "", view, next.Meta.Revision) {
				return
			}
			if view.State == game.Open && view.Question != nil {
				send("question_timer", "", map[string]string{"question_id": view.Question.ID, "server_time": view.ServerTime, "deadline": view.Question.Deadline}, next.Meta.Revision)
			}
			revision = next.Meta.Revision
			lastControl = next.Meta.Control
			lastMembers = len(next.Players)
			previousPlayers = next.Players
			lastAnswer = ownCommand
			lastSnapshot = time.Now()
		case item := <-input:
			if item.err != nil {
				return
			}
			if item.kind != websocket.TextMessage {
				closeSocket(1003)
				return
			}
			cmd, decodeErr := s.decode(item.message)
			if decodeErr != nil {
				closeSocket(1008)
				return
			}
			if cmd.PIN != b.pin || cmd.Session != b.id || cmd.Player != b.player {
				sendError(cmd, game.Fail("forbidden"))
				continue
			}
			err = s.Store.Limit(ctx, "member", b.id+":"+b.role+":"+b.player, 120)
			if err != nil {
				sendError(cmd, err)
				continue
			}
			switch cmd.Type {
			case "submit_answer":
				if b.role != "player" {
					err = game.Fail("forbidden")
					break
				}
				start := time.Now()
				var payload struct {
					Question string `json:"question_id"`
					Option   string `json:"option_id"`
				}
				_ = json.Unmarshal(cmd.Payload, &payload)
				var a game.Answer
				var rev int64
				a, rev, err = s.Store.Submit(ctx, b.pin, b.id, b.player, b.digest, b.connection, cmd.ID, payload.Question, payload.Option)
				s.answerLatency.Observe(time.Since(start).Seconds())
				if err == nil {
					send("answer_received", cmd.ID, a.Public(), rev)
				}
			case "start_game", "next_question", "end_game":
				if b.role != "host" {
					err = game.Fail("forbidden")
					break
				}
				err = s.admin(b.admin, "", false)
				if err != nil {
					break
				}
				err = s.Store.Limit(ctx, "host_control", "admin", 60)
				if err != nil {
					break
				}
				var payload struct {
					Expected int64 `json:"expected_control_revision"`
				}
				_ = json.Unmarshal(cmd.Payload, &payload)
				state, err = s.Engine.Control(ctx, b.pin, b.id, b.capability, cmd.Type, cmd.ID, payload.Expected)
				if err == nil {
					send("command_accepted", cmd.ID, nil, state.Meta.Revision)
				}
			case "leave_game":
				if b.role != "player" {
					err = game.Fail("forbidden")
					break
				}
				err = s.Engine.Leave(ctx, b.pin, b.id, b.player, b.digest, b.connection, cmd.ID)
				if err == nil {
					send("game_left", cmd.ID, nil, revision)
					closeSocket(1000)
					return
				}
			case "ping", "request_snapshot":
				state, err = s.Store.Read(ctx, b.pin, b.id, b.player, "")
				if err != nil {
					break
				}
				if b.role == "player" {
					_, err = state.Authenticate(b.player, b.digest, b.connection)
				} else {
					err = s.admin(b.admin, "", false)
				}
				if err != nil {
					break
				}
				if cmd.Type == "ping" {
					send("pong", cmd.ID, map[string]any{"server_time": game.Time(state.Now)}, state.Meta.Revision)
				} else {
					send("state_snapshot", cmd.ID, state.Snapshot(b.role, b.player), state.Meta.Revision)
				}
			default:
				err = game.Fail("invalid_state")
			}
			if err != nil {
				sendError(cmd, err)
			}
		}
	}
}

func stringLocal(c *websocket.Conn, key string) string {
	value, _ := c.Locals(key).(string)
	return value
}

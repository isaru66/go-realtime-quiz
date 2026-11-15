package game

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"github.com/isaru66/go-realtime-quiz/backend/internal/config"
)

const (
	Lobby    = "LOBBY"
	Open     = "QUESTION_OPEN"
	Closed   = "QUESTION_CLOSED"
	Finished = "FINISHED"
	Expired  = "EXPIRED"
)

type Error struct{ Code string }

func (e *Error) Error() string { return e.Code }
func Fail(code string) error   { return &Error{code} }

type Meta struct {
	IdleMillis      int64  `json:"idle_ms"`
	LifetimeMillis  int64  `json:"lifetime_ms"`
	RetentionMillis int64  `json:"retention_ms"`
	PIN             string `json:"pin"`
	ID              string `json:"id"`
	QuizID          string `json:"quiz_id"`
	Version         string `json:"version"`
	HostDigest      string `json:"host_digest"`
	State           string `json:"state"`
	Revision        int64  `json:"revision"`
	Control         int64  `json:"control"`
	Index           int    `json:"index"`
	Started         int64  `json:"started"`
	Deadline        int64  `json:"deadline"`
	ClosedAt        int64  `json:"closed_at"`
	Created         int64  `json:"created"`
	Activity        int64  `json:"activity"`
	FinishedAt      int64  `json:"finished_at"`
	Expires         int64  `json:"expires"`
	Purge           int64  `json:"purge"`
}

type Member struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Digest     string `json:"digest"`
	Generation int64  `json:"generation"`
	Connection string `json:"connection"`
	Presence   int64  `json:"presence"`
	Left       bool   `json:"left"`
}

type Answer struct {
	Question   string `json:"question_id"`
	Option     string `json:"option_id"`
	Command    string `json:"command"`
	AcceptedAt int64  `json:"accepted_at"`
	Correct    bool   `json:"correct"`
	Points     int64  `json:"points"`
}

type State struct {
	Meta          Meta
	Quiz          config.Quiz
	Players       map[string]Member
	Answers       map[string]Answer
	Scores        map[string]int64
	Published     map[string]int64
	Receipt       string
	CommandCount  int
	AnswerReceipt *Answer
	Now           int64
}

func Digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func EqualDigest(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func Time(ms int64) string         { return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z") }

type PublicMember struct {
	ID        string `json:"player_id"`
	Name      string `json:"player_name"`
	Connected bool   `json:"connected"`
	Left      bool   `json:"left"`
}
type Rank struct {
	ID    string `json:"player_id"`
	Name  string `json:"player_name"`
	Score int64  `json:"score"`
	Rank  int    `json:"rank"`
	Left  bool   `json:"left"`
}
type PublicQuestion struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Options  []config.Option `json:"options"`
	Index    int             `json:"index"`
	Total    int             `json:"total"`
	Points   int64           `json:"points"`
	Started  string          `json:"started_at"`
	Deadline string          `json:"deadline"`
}
type Accepted struct {
	Question string `json:"question_id"`
	Accepted bool   `json:"accepted"`
	Option   string `json:"option_id"`
	At       string `json:"accepted_at"`
}

func (a Answer) Public() Accepted { return Accepted{a.Question, true, a.Option, Time(a.AcceptedAt)} }

type Result struct {
	Question      string `json:"question_id"`
	CorrectOption string `json:"correct_option_id"`
	Answered      bool   `json:"answered"`
	Correct       bool   `json:"correct"`
	Points        int64  `json:"awarded_points"`
}
type Closure struct {
	Question      string `json:"question_id"`
	CorrectOption string `json:"correct_option_id"`
	At            string `json:"closed_at"`
}
type Statistics struct {
	Question string         `json:"question_id"`
	Counts   map[string]int `json:"counts"`
	Count    int            `json:"answer_count"`
}
type Snapshot struct {
	State        string          `json:"state"`
	Role         string          `json:"role"`
	QuizID       string          `json:"quiz_id"`
	Version      string          `json:"quiz_version"`
	ServerTime   string          `json:"server_time"`
	Expires      string          `json:"expires_at"`
	Control      int64           `json:"control_revision"`
	Roster       []PublicMember  `json:"roster"`
	Leaderboard  []Rank          `json:"leaderboard"`
	Question     *PublicQuestion `json:"question,omitempty"`
	OwnAnswer    *Accepted       `json:"own_answer,omitempty"`
	OwnScore     *int64          `json:"own_score,omitempty"`
	OwnResult    *Result         `json:"own_result,omitempty"`
	ClosedResult *Closure        `json:"closed_result,omitempty"`
	Statistics   *Statistics     `json:"statistics,omitempty"`
}

func (s *State) Snapshot(role, player string) Snapshot {
	m := s.Meta
	view := Snapshot{State: m.State, Role: role, QuizID: m.QuizID, Version: m.Version, ServerTime: Time(s.Now), Expires: Time(m.Expires), Control: m.Control, Roster: []PublicMember{}, Leaderboard: []Rank{}}
	if m.State == Expired || s.Now >= m.Expires {
		view.State = Expired
		return view
	}
	for id, member := range s.Players {
		view.Roster = append(view.Roster, PublicMember{id, member.Name, !member.Left && member.Presence > s.Now, member.Left})
		view.Leaderboard = append(view.Leaderboard, Rank{ID: id, Name: member.Name, Score: s.Published[id], Left: member.Left})
	}
	sort.Slice(view.Roster, func(i, j int) bool { return view.Roster[i].ID < view.Roster[j].ID })
	sort.Slice(view.Leaderboard, func(i, j int) bool {
		a, b := view.Leaderboard[i], view.Leaderboard[j]
		if a.Score == b.Score {
			return a.ID < b.ID
		}
		return a.Score > b.Score
	})
	for i := range view.Leaderboard {
		view.Leaderboard[i].Rank = i + 1
		if i > 0 && view.Leaderboard[i].Score == view.Leaderboard[i-1].Score {
			view.Leaderboard[i].Rank = view.Leaderboard[i-1].Rank
		}
	}
	if role == "player" {
		score := s.Published[player]
		view.OwnScore = &score
	}
	if m.Index < 0 || m.Index >= len(s.Quiz.Questions) {
		return view
	}
	q := s.Quiz.Questions[m.Index]
	view.Question = &PublicQuestion{q.ID, q.Type, q.Text, q.Options, m.Index, len(s.Quiz.Questions), q.Points, Time(m.Started), Time(m.Deadline)}
	if role == "player" {
		if a, ok := s.Answers[player]; ok {
			v := a.Public()
			view.OwnAnswer = &v
		}
	}
	if m.State == Closed || m.State == Finished {
		view.ClosedResult = &Closure{q.ID, q.Correct, Time(m.ClosedAt)}
		if role == "player" {
			a, ok := s.Answers[player]
			view.OwnResult = &Result{q.ID, q.Correct, ok, a.Correct, a.Points}
		} else {
			stats := &Statistics{Question: q.ID, Counts: map[string]int{}}
			for _, o := range q.Options {
				stats.Counts[o.ID] = 0
			}
			for _, a := range s.Answers {
				stats.Counts[a.Option]++
				stats.Count++
			}
			view.Statistics = stats
		}
	}
	return view
}

func (s *State) Authenticate(player, digest, connection string) (Member, error) {
	m, ok := s.Players[player]
	if !ok || !EqualDigest(m.Digest, digest) {
		return Member{}, Fail("invalid_credentials")
	}
	if s.Meta.State == Expired || s.Now >= s.Meta.Expires {
		return m, Fail("session_expired")
	}
	if m.Left {
		return m, Fail("membership_left")
	}
	if connection != "" && m.Connection != connection {
		return m, Fail("connection_replaced")
	}
	return m, nil
}

func AnswerField(question, player string) string { return fmt.Sprintf("q:%s:%s", question, player) }

package game

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/isaru66/go-realtime-quiz/backend/internal/config"
)

func TestSnapshotRedactionAndClosure(t *testing.T) {
	s := &State{
		Meta:    Meta{State: Open, Index: 0, Expires: 10000, Deadline: 5000, Started: 1000},
		Quiz:    config.Quiz{Questions: []config.Question{{ID: "q1", Type: "single_choice", Text: "Question", Correct: "A", Points: 1000, Options: []config.Option{{ID: "A", Text: "First"}, {ID: "B", Text: "Second"}}}}},
		Players: map[string]Member{"p": {Name: "Player", Digest: "secret-digest", Connection: "private-connection"}, "other": {Name: "Other"}},
		Answers: map[string]Answer{"p": {Question: "q1", Option: "A", Correct: true, Points: 1000, AcceptedAt: 2000}},
		Scores:  map[string]int64{"p": 1000}, Published: map[string]int64{"p": 0, "other": 0}, Now: 3000,
	}
	for _, role := range []string{"player", "host"} {
		view := s.Snapshot(role, "p")
		encoded, _ := json.Marshal(view)
		for _, secret := range []string{"correct_answer", "correct_option_id", "awarded_points", "secret-digest", "private-connection"} {
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("%s snapshot leaked %s", role, secret)
			}
		}
		if view.Leaderboard[0].Score != 0 {
			t.Fatal("open-question score leaked")
		}
		if view.Leaderboard[0].Rank != 1 || view.Leaderboard[1].Rank != 1 {
			t.Fatal("tied ranks differ")
		}
	}
	s.Meta.State = Closed
	s.Published["p"] = 1000
	player := s.Snapshot("player", "p")
	host := s.Snapshot("host", "")
	if player.OwnResult == nil || !player.OwnResult.Correct || *player.OwnScore != 1000 || player.Statistics != nil {
		t.Fatal("player closure projection incorrect")
	}
	if host.OwnResult != nil || host.Statistics.Counts["A"] != 1 {
		t.Fatal("host closure projection incorrect")
	}
	s.Now = 10000
	expired := s.Snapshot("player", "p")
	if expired.Question != nil || expired.OwnScore != nil || len(expired.Roster) != 0 || len(expired.Leaderboard) != 0 {
		t.Fatal("expired state retained public gameplay details")
	}
}
func TestAuthenticationRequiresCredentialAndCurrentConnection(t *testing.T) {
	s := &State{Meta: Meta{Expires: 100}, Now: 10, Players: map[string]Member{"p": {Digest: Digest("token"), Connection: "new"}}}
	if _, err := s.Authenticate("p", Digest("wrong"), "new"); err == nil {
		t.Fatal("player ID alone authorized")
	}
	if _, err := s.Authenticate("p", Digest("token"), "old"); err == nil {
		t.Fatal("old connection not fenced")
	}
	if _, err := s.Authenticate("p", Digest("token"), "new"); err != nil {
		t.Fatal(err)
	}
}

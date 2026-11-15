//go:build integration

package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/google/uuid"
	"github.com/isaru66/go-realtime-quiz/backend/internal/config"
	"github.com/isaru66/go-realtime-quiz/backend/internal/game"
	repository "github.com/isaru66/go-realtime-quiz/backend/internal/redis"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

type testSocket struct {
	c               *websocket.Conn
	t               *testing.T
	pin, id, player string
	schema          *jsonschema.Schema
}

func (c *testSocket) send(kind string, payload any) string {
	c.t.Helper()
	id := uuid.NewString()
	data := map[string]any{"event_id": id, "event_type": kind, "protocol_version": 1, "game_pin": c.pin, "timestamp": game.Time(time.Now().UnixMilli()), "payload": payload}
	if c.id != "" {
		data["session_id"] = c.id
	}
	if c.player != "" {
		data["player_id"] = c.player
	}
	if err := c.c.WriteJSON(data); err != nil {
		c.t.Fatal(err)
	}
	return id
}
func (c *testSocket) await(kind string) map[string]any {
	c.t.Helper()
	for {
		_ = c.c.SetReadDeadline(time.Now().Add(50 * time.Second))
		_, data, err := c.c.ReadMessage()
		if err != nil {
			c.t.Fatalf("await %s: %v", kind, err)
		}
		var raw any
		if err = json.Unmarshal(data, &raw); err != nil {
			c.t.Fatal(err)
		}
		if err = c.schema.Validate(raw); err != nil {
			c.t.Fatalf("invalid server %s: %v\n%s", kind, err, data)
		}
		msg := raw.(map[string]any)
		if msg["event_type"] == kind {
			return msg
		}
		if msg["event_type"] == "error" || msg["event_type"] == "reconnect_failed" {
			c.t.Fatalf("unexpected server error awaiting %s: %v", kind, msg["payload"])
		}
	}
}
func (c *testSocket) snapshot(state string) map[string]any {
	c.t.Helper()
	for {
		event := c.await("state_snapshot")
		payload := event["payload"].(map[string]any)
		if payload["state"] == state {
			return payload
		}
	}
}
func (c *testSocket) question(state, id string) map[string]any {
	c.t.Helper()
	for {
		view := c.snapshot(state)
		q, ok := view["question"].(map[string]any)
		if ok && q["id"] == id {
			return view
		}
	}
}

func TestDistributedLifecycleAndRecovery(t *testing.T) {
	a, b := os.Getenv("BACKEND_A_URL"), os.Getenv("BACKEND_B_URL")
	if a == "" || b == "" {
		t.Skip("set BACKEND_A_URL and BACKEND_B_URL for real-cluster tests")
	}
	origin := "http://localhost:8088"
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	request := func(method, url string, body any, csrf, key string) map[string]any {
		t.Helper()
		encoded, _ := json.Marshal(body)
		req, err := http.NewRequest(method, url, bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 300 {
			t.Fatalf("%s %s => %d %s", method, url, resp.StatusCode, data)
		}
		var result map[string]any
		if err = json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	login := request("POST", a+"/api/v1/auth/session", map[string]string{"credential": os.Getenv("ADMIN_CREDENTIAL")}, "", "")
	csrf := login["csrf_token"].(string)
	catalog := request("GET", a+"/api/v1/quizzes", nil, "", "")
	version := catalog["catalog_version"].(string)
	key := uuid.NewString()
	created := request("POST", a+"/api/v1/games", map[string]string{"quiz_id": "azure-fundamentals", "quiz_version": version}, csrf, key)
	repeated := request("POST", b+"/api/v1/games", map[string]string{"quiz_id": "azure-fundamentals", "quiz_version": version}, csrf, key)
	if created["session_id"] != repeated["session_id"] || created["host_capability"] != repeated["host_capability"] {
		t.Fatal("creation retry produced a different game/capability")
	}
	pin, sid := created["game_pin"].(string), created["session_id"].(string)
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat = true
	schema, err := compiler.Compile("../../../../docs/protocol-v1.schema.json#/$defs/server")
	if err != nil {
		t.Fatal(err)
	}
	dial := func(base, player string, host bool) *testSocket {
		t.Helper()
		headers := http.Header{"Origin": []string{origin}}
		if host {
			url, _ := http.NewRequest("GET", base, nil)
			for _, cookie := range jar.Cookies(url.URL) {
				headers.Add("Cookie", cookie.String())
			}
		}
		d := websocket.Dialer{Subprotocols: []string{"quiz.v1"}, HandshakeTimeout: 5 * time.Second}
		conn, resp, err := d.Dial(strings.Replace(base, "http", "ws", 1)+"/ws", headers)
		if err != nil {
			if resp != nil {
				t.Fatalf("dial %s: %v (status %d)", base, err, resp.StatusCode)
			}
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return &testSocket{conn, t, pin, sid, player, schema}
	}
	host := dial(a, "", true)
	host.send("host_connect", map[string]string{"host_capability": created["host_capability"].(string), "csrf_token": csrf})
	host.await("host_connected")
	const count = 12
	players := make([]*testSocket, count)
	tokens := make([]string, count)
	for i := range players {
		base := a
		if i%2 == 1 {
			base = b
		}
		players[i] = dial(base, uuid.NewString(), false)
		tokens[i] = repository.Token()
		players[i].send("join_game", map[string]string{"player_name": fmt.Sprintf("Player%d", i), "reconnect_token": tokens[i]})
		players[i].await("game_joined")
	}
	// No question has started; the server must reject attempts to answer.
	players[0].send("submit_answer", map[string]string{"question_id": "q1", "option_id": "A"})
	pre := players[0].await("error")["payload"].(map[string]any)
	if pre["code"] != "invalid_state" {
		t.Fatalf("pre-start submission: %v", pre)
	}
	data, err := os.ReadFile("../../../../config/quizzes.yaml")
	if err != nil {
		t.Fatal(err)
	}
	quizCatalog, err := config.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	var quiz config.Quiz
	for _, q := range quizCatalog.Quizzes {
		if q.ID == "azure-fundamentals" {
			quiz = q
		}
	}
	var expected int64
	host.send("start_game", map[string]int64{"expected_control_revision": 0})
	host.await("command_accepted")
	for index, q := range quiz.Questions {
		for _, p := range players {
			p.question(game.Open, q.ID)
		}
		var wg sync.WaitGroup
		for _, p := range players {
			p := p
			wg.Add(1)
			go func() {
				defer wg.Done()
				id := uuid.NewString()
				message := map[string]any{"event_id": id, "event_type": "submit_answer", "protocol_version": 1, "game_pin": pin, "session_id": sid, "player_id": p.player, "timestamp": game.Time(time.Now().UnixMilli()), "payload": map[string]string{"question_id": q.ID, "option_id": "A"}}
				for retry := 0; retry < 2; retry++ {
					if err := p.c.WriteJSON(message); err != nil {
						t.Error(err)
						return
					}
				}
				p.await("answer_received")
			}()
		}
		wg.Wait()
		if index == 0 {
			players[0].send("submit_answer", map[string]string{"question_id": q.ID, "option_id": "B"})
			if code := players[0].await("error")["payload"].(map[string]any)["code"]; code != "already_answered" {
				t.Fatalf("duplicate acceptance: %v", code)
			}
			old := players[0]
			recovered := dial(b, old.player, false)
			recovered.send("reconnect", map[string]any{"reconnect_token": tokens[0], "last_revision": 1})
			view := recovered.await("reconnect_success")["payload"].(map[string]any)
			if len(view["roster"].([]any)) != count || view["own_score"] != float64(0) || view["own_answer"] == nil {
				t.Fatalf("recovery duplicated player, lost answer, or leaked score: %v", view)
			}
			players[0] = recovered
			_ = old.c.Close()
		}
		closed := host.question(game.Closed, q.ID)
		if q.Correct == "A" {
			expected += q.Points
		}
		board := closed["leaderboard"].([]any)
		if len(board) != count {
			t.Fatal("leaderboard membership changed")
		}
		for _, rank := range board {
			if rank.(map[string]any)["score"] != float64(expected) {
				t.Fatalf("duplicate or inconsistent score: %v expected %d", rank, expected)
			}
		}
		if index == 0 && os.Getenv("QUIZ_ALLOW_FAILURE_TESTS") == "true" {
			restartCompose(t, "restart", "backend2")
			for i, p := range players {
				_ = p.c.Close()
				players[i] = dial(a, p.player, false)
				players[i].send("reconnect", map[string]any{"reconnect_token": tokens[i]})
				view := players[i].await("reconnect_success")["payload"].(map[string]any)
				if view["own_score"] != float64(expected) || len(view["roster"].([]any)) != count {
					t.Fatal("backend restart lost identity/score")
				}
			}
			t.Log("backend2 restart recovered every identity, score, and answer on backend A")
		}
		host.send("next_question", map[string]any{"expected_control_revision": closed["control_revision"]})
		host.await("command_accepted")
	}
	final := host.snapshot(game.Finished)
	if len(final["leaderboard"].([]any)) != count {
		t.Fatal("final roster changed")
	}
	if service := os.Getenv("QUIZ_FAIL_REDIS_SERVICE"); service != "" && os.Getenv("QUIZ_ALLOW_FAILURE_TESTS") == "true" {
		service = owningPrimary(t, pin, sid)
		t.Logf("failing owning Redis primary %s", service)
		restartCompose(t, "stop", service)
		defer restartCompose(t, "start", service)
		deadline := time.Now().Add(45 * time.Second)
		for {
			response, err := client.Get(a + "/readyz")
			if err == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("Redis Cluster did not recover readiness after primary failure")
			}
			time.Sleep(time.Second)
		}
		for _, i := range []int{0, 2} {
			old := players[i]
			_ = old.c.Close()
			players[i] = dial(a, old.player, false)
			players[i].send("reconnect", map[string]string{"reconnect_token": tokens[i]})
			view := players[i].await("reconnect_success")["payload"].(map[string]any)
			if view["own_score"] != float64(expected) || len(view["roster"].([]any)) != count {
				t.Fatal("Redis failover lost replicated identity/score")
			}
		}
		t.Log("Redis primary failure recovered readiness and published scores through a promoted replica")
	}
	// Simulate a lost leave acknowledgment; reconnect must confirm the prior leave.
	departed := players[0]
	departed.send("leave_game", map[string]any{})
	departed.await("game_left")
	_ = departed.c.Close()
	left := dial(a, departed.player, false)
	left.send("reconnect", map[string]string{"reconnect_token": tokens[0]})
	left.await("game_left")
	t.Logf("completed %d questions, %d players across two backend processes, duplicate retries, snapshots, fencing and leave recovery", len(quiz.Questions), count)
}

func restartCompose(t *testing.T, args ...string) {
	t.Helper()
	composeOutput(t, args...)
	time.Sleep(3 * time.Second)
}

func owningPrimary(t *testing.T, pin, id string) string {
	t.Helper()
	for i := 0; i < 6; i++ {
		service := fmt.Sprintf("redis-%d", i)
		output := composeOutput(t, "exec", "-T", service, "sh", "-c", `export REDISCLI_AUTH="$REDIS_PASSWORD"; redis-cli --raw ROLE | head -n 1; redis-cli --raw EXISTS "$1"`, "check", repository.Keys(pin, id)[1])
		if strings.TrimSpace(output) == "master\n1" {
			return service
		}
	}
	t.Fatal("could not identify the game's owning primary")
	return ""
}

func composeOutput(t *testing.T, args ...string) string {
	t.Helper()
	project, env := os.Getenv("QUIZ_COMPOSE_PROJECT"), os.Getenv("QUIZ_COMPOSE_ENV")
	if !strings.HasPrefix(project, "quiz-session-") || env == "" {
		t.Fatal("failure tests require isolated quiz-session-* project and QUIZ_COMPOSE_ENV")
	}
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	bin := os.Getenv("DOCKER_COMPOSE_BIN")
	prefix := []string{"compose"}
	if bin == "" {
		bin = "docker"
	} else {
		prefix = nil
	}
	command := append(prefix, "--env-file", env, "-p", project, "-f", filepath.Join(root, "deploy/compose/compose.yaml"))
	cmd := exec.CommandContext(ctx, bin, append(command, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compose failure test: %v: %s", err, output)
	}
	return string(output)
}

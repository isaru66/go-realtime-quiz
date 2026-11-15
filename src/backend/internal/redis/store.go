package redis

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/isaru66/go-realtime-quiz/backend/internal/config"
	"github.com/isaru66/go-realtime-quiz/backend/internal/game"
	goredis "github.com/redis/go-redis/v9"
)

type Store struct {
	Client          *goredis.ClusterClient
	secret          []byte
	Log             *slog.Logger
	Observe         func(string, time.Duration)
	IdleMillis      int64
	LifetimeMillis  int64
	RetentionMillis int64
}

func New(addresses, password, secret string, tlsEnabled bool, caFile string, log *slog.Logger) (*Store, error) {
	opt := &goredis.ClusterOptions{Addrs: strings.Split(addresses, ","), Password: password, PoolSize: 32, MinIdleConns: 2, DialTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, MaxRetries: 2, MaxRedirects: 8}
	if tlsEnabled {
		t := &tls.Config{MinVersion: tls.VersionTLS12}
		if caFile != "" {
			data, err := os.ReadFile(caFile)
			if err != nil {
				return nil, err
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(data) {
				return nil, fmt.Errorf("invalid Redis CA")
			}
			t.RootCAs = pool
		}
		opt.TLSConfig = t
	}
	if len(secret) < 32 || password == "" {
		return nil, fmt.Errorf("APP_SECRET >=32 characters and REDIS_PASSWORD are required")
	}
	idle, err := policy("GAME_IDLE_SECONDS", 7200)
	if err != nil {
		return nil, err
	}
	lifetime, err := policy("GAME_LIFETIME_SECONDS", 86400)
	if err != nil {
		return nil, err
	}
	retention, err := policy("RESULT_RETENTION_SECONDS", 3600)
	if err != nil {
		return nil, err
	}
	if idle > lifetime || retention > lifetime {
		return nil, fmt.Errorf("idle/result retention cannot exceed absolute lifetime")
	}
	return &Store{Client: goredis.NewClusterClient(opt), secret: []byte(secret), Log: log, IdleMillis: idle * 1000, LifetimeMillis: lifetime * 1000, RetentionMillis: retention * 1000}, nil
}
func policy(name string, fallback int64) (int64, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 1 || n > 604800 {
		return 0, fmt.Errorf("%s must be between 1 and 604800 seconds", name)
	}
	return n, nil
}

func (s *Store) observe(op string, start time.Time) {
	if s.Observe != nil {
		s.Observe(op, time.Since(start))
	}
}
func Prefix(pin string) string {
	n, _ := strconv.Atoi(pin)
	return fmt.Sprintf("quiz:v1:{quiz:p%02d}:", n%64)
}
func Partition(p int) string { return fmt.Sprintf("quiz:v1:{quiz:p%02d}:", p) }
func Keys(pin, id string) []string {
	p := Prefix(pin)
	return []string{p + "pin:" + pin, p + "game:" + id, p + "quiz:" + id, p + "players:" + id, p + "participants:" + id, p + "answers:" + id, p + "scores:" + id, p + "published:" + id, p + "events:" + id, p + "commands:" + id, p + "due", p + "active"}
}
func Channel(pin, id string) string { return Prefix(pin) + "room:" + id }

func (s *Store) Lookup(ctx context.Context, pin string) (string, error) {
	id, err := s.Client.Get(ctx, Prefix(pin)+"pin:"+pin).Result()
	if err == goredis.Nil {
		return "", game.Fail("wrong_pin")
	}
	return id, err
}

type flat []string

func (f *flat) UnmarshalJSON(data []byte) error {
	if string(data) == "{}" {
		*f = nil
		return nil
	}
	return json.Unmarshal(data, (*[]string)(f))
}

func (s *Store) Read(ctx context.Context, pin, id, principal, command string) (*game.State, error) {
	start := time.Now()
	defer s.observe("snapshot", start)
	raw, err := s.Client.Eval(ctx, snapshotScript, Keys(pin, id), principal, command).Text()
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, game.Fail("session_unavailable")
	}
	var row struct {
		Meta          string `json:"meta"`
		Quiz          string `json:"quiz"`
		Players       flat   `json:"players"`
		Answers       flat   `json:"answers"`
		Scores        flat   `json:"scores"`
		Published     flat   `json:"published"`
		Receipt       string `json:"receipt"`
		AnswerReceipt string `json:"answer_receipt"`
		CommandCount  int    `json:"command_count"`
		Now           int64  `json:"now"`
	}
	if err = json.Unmarshal([]byte(raw), &row); err != nil {
		return nil, err
	}
	state := &game.State{Players: map[string]game.Member{}, Answers: map[string]game.Answer{}, Scores: map[string]int64{}, Published: map[string]int64{}, Receipt: row.Receipt, CommandCount: row.CommandCount, Now: row.Now}
	if err = json.Unmarshal([]byte(row.Meta), &state.Meta); err != nil {
		return nil, err
	}
	if state.Meta.PIN != pin || state.Meta.ID != id {
		return nil, game.Fail("session_mismatch")
	}
	if err = json.Unmarshal([]byte(row.Quiz), &state.Quiz); err != nil {
		return nil, err
	}
	for i := 0; i < len(row.Players); i += 2 {
		var member game.Member
		if err = json.Unmarshal([]byte(row.Players[i+1]), &member); err != nil {
			return nil, err
		}
		state.Players[row.Players[i]] = member
	}
	for i := 0; i < len(row.Answers); i += 2 {
		var a game.Answer
		if err = json.Unmarshal([]byte(row.Answers[i+1]), &a); err != nil {
			return nil, err
		}
		state.Answers[row.Answers[i]] = a
	}
	for i := 0; i < len(row.Scores); i += 2 {
		n, e := strconv.ParseInt(row.Scores[i+1], 10, 64)
		if e != nil {
			return nil, e
		}
		state.Scores[row.Scores[i]] = n
	}
	for i := 0; i < len(row.Published); i += 2 {
		n, e := strconv.ParseInt(row.Published[i+1], 10, 64)
		if e != nil {
			return nil, e
		}
		state.Published[row.Published[i]] = n
	}
	if row.AnswerReceipt != "" {
		var a game.Answer
		if err = json.Unmarshal([]byte(row.AnswerReceipt), &a); err != nil {
			return nil, err
		}
		state.AnswerReceipt = &a
	}
	return state, nil
}

func (s *Store) Commit(ctx context.Context, state *game.State, m game.Mutation) error {
	start := time.Now()
	defer s.observe("mutation", start)
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	result, err := s.Client.Eval(ctx, mutateScript, Keys(state.Meta.PIN, state.Meta.ID), state.Meta.Revision, string(data)).Text()
	if err != nil {
		return err
	}
	if result != "ok" {
		return game.Fail(result)
	}
	s.Notify(ctx, state.Meta.PIN, state.Meta.ID)
	return nil
}
func (s *Store) Notify(ctx context.Context, pin, id string) {
	if err := s.Client.Publish(ctx, Channel(pin, id), "changed").Err(); err != nil {
		s.Log.Warn("room notification failed; reconciliation will recover", "game_pin", pin, "session_id", id)
	}
}
func (s *Store) Heartbeat(ctx context.Context, pin, id, player, connection string) (bool, error) {
	n, err := s.Client.Eval(ctx, heartbeatScript, []string{Keys(pin, id)[3]}, player, connection).Int()
	return n == 1, err
}
func (s *Store) Limit(ctx context.Context, scope, identity string, max int) error {
	digest := game.Digest(identity)
	n, _ := strconv.ParseUint(digest[:2], 16, 8)
	p := int(n) % 64
	value, err := s.Client.Eval(ctx, limitScript, []string{Partition(p) + "limit:" + scope + ":" + digest}, max).Int()
	if err != nil {
		return err
	}
	if value == 0 {
		return game.Fail("rate_limited")
	}
	return nil
}

type Creation struct {
	ID         string `json:"session_id"`
	PIN        string `json:"game_pin"`
	QuizID     string `json:"quiz_id"`
	Version    string `json:"quiz_version"`
	Capability string `json:"host_capability"`
	Expires    string `json:"expires_at"`
	Control    int64  `json:"control_revision"`
}
type receipt struct {
	Fingerprint string `json:"fingerprint"`
	ID          string `json:"id"`
	PIN         string `json:"pin"`
	QuizID      string `json:"quiz_id"`
	Version     string `json:"version"`
	Cipher      string `json:"cipher"`
	Expires     int64  `json:"expires"`
}

func Token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func (s *Store) aead() (cipher.AEAD, error) {
	sum := sha256.Sum256(s.secret)
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func (s *Store) encrypt(token, id string) (string, error) {
	g, err := s.aead()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(g.Seal(nonce, nonce, []byte(token), []byte(id))), nil
}
func (s *Store) creation(ctx context.Context, raw, fingerprint string) (Creation, error) {
	var r receipt
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return Creation{}, err
	}
	if r.Fingerprint != fingerprint {
		return Creation{}, game.Fail("idempotency_conflict")
	}
	st, err := s.Read(ctx, r.PIN, r.ID, "", "")
	if err != nil {
		return Creation{}, err
	}
	if st.Now >= st.Meta.Expires || st.Meta.State == game.Expired {
		return Creation{}, game.Fail("session_expired")
	}
	g, err := s.aead()
	if err != nil {
		return Creation{}, err
	}
	encoded, err := base64.RawURLEncoding.DecodeString(r.Cipher)
	if err != nil || len(encoded) < g.NonceSize() {
		return Creation{}, fmt.Errorf("invalid creation receipt")
	}
	token, err := g.Open(nil, encoded[:g.NonceSize()], encoded[g.NonceSize():], []byte(r.ID))
	if err != nil {
		return Creation{}, err
	}
	return Creation{r.ID, r.PIN, r.QuizID, r.Version, string(token), game.Time(r.Expires), 0}, nil
}
func (s *Store) Create(ctx context.Context, key, quizID, version string, catalog *config.Catalog) (Creation, error) {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte("admin:" + key))
	p := int(mac.Sum(nil)[0]) % 64
	receiptKey := Partition(p) + "create:admin:" + game.Digest(key)
	fingerprint := game.Digest(quizID + ":" + version)
	if raw, err := s.Client.Get(ctx, receiptKey).Result(); err == nil {
		return s.creation(ctx, raw, fingerprint)
	} else if err != goredis.Nil {
		return Creation{}, err
	}
	if catalog == nil {
		return Creation{}, game.Fail("temporarily_unavailable")
	}
	if catalog.Version != version {
		return Creation{}, game.Fail("configuration_changed")
	}
	var quiz *config.Quiz
	for i := range catalog.Quizzes {
		if catalog.Quizzes[i].ID == quizID {
			quiz = &catalog.Quizzes[i]
			break
		}
	}
	if quiz == nil {
		return Creation{}, game.Fail("quiz_not_found")
	}
	for attempt := 0; attempt < 32; attempt++ {
		first := 100000 + (p-100000%64+64)%64
		count := (999999-first)/64 + 1
		n, err := rand.Int(rand.Reader, big.NewInt(int64(count)))
		if err != nil {
			return Creation{}, err
		}
		pin := strconv.Itoa(first + int(n.Int64())*64)
		id := uuid.NewString()
		token := Token()
		m := game.Meta{PIN: pin, ID: id, QuizID: quiz.ID, Version: version, HostDigest: game.Digest(token), State: game.Lobby, Revision: 1, Index: -1, IdleMillis: s.IdleMillis, LifetimeMillis: s.LifetimeMillis, RetentionMillis: s.RetentionMillis}
		encrypted, err := s.encrypt(token, id)
		if err != nil {
			return Creation{}, err
		}
		r := receipt{Fingerprint: fingerprint, ID: id, PIN: pin, QuizID: quiz.ID, Version: version, Cipher: encrypted}
		mapped, err := s.Client.Get(ctx, Prefix(pin)+"pin:"+pin).Result()
		if err != nil && err != goredis.Nil {
			return Creation{}, err
		}
		keys := append(Keys(pin, id), receiptKey, Prefix(pin)+"game:"+mapped)
		meta, _ := json.Marshal(m)
		q, _ := json.Marshal(quiz)
		rec, _ := json.Marshal(r)
		raw, err := s.Client.Eval(ctx, createScript, keys, string(meta), string(q), string(rec), mapped).Text()
		if err != nil {
			return Creation{}, err
		}
		if raw == "retry" || raw == "collision" {
			continue
		}
		return s.creation(ctx, raw, fingerprint)
	}
	return Creation{}, game.Fail("capacity_exceeded")
}

func (s *Store) Due(ctx context.Context, partition int) ([]string, error) {
	return s.Client.Eval(ctx, dueScript, []string{Partition(partition) + "due"}).StringSlice()
}
func (s *Store) RemoveDue(ctx context.Context, pin, id string) error {
	member := pin + ":" + id
	_, err := s.Client.TxPipelined(ctx, func(p goredis.Pipeliner) error {
		p.ZRem(ctx, Prefix(pin)+"due", member)
		p.ZRem(ctx, Prefix(pin)+"active", member)
		return nil
	})
	return err
}

func (s *Store) Submit(ctx context.Context, pin, id, player, digest, connection, command, question, option string) (game.Answer, int64, error) {
	start := time.Now()
	defer s.observe("answer", start)
	raw, err := s.Client.Eval(ctx, answerScript, Keys(pin, id), player, digest, connection, command, question, option).Text()
	if err != nil {
		return game.Answer{}, 0, err
	}
	var result struct {
		Answer   game.Answer `json:"answer"`
		Revision int64       `json:"revision"`
		Error    string      `json:"error"`
	}
	if err = json.Unmarshal([]byte(raw), &result); err != nil {
		return game.Answer{}, 0, err
	}
	if result.Error != "" {
		return game.Answer{}, 0, game.Fail(result.Error)
	}
	s.Notify(ctx, pin, id)
	return result.Answer, result.Revision, nil
}

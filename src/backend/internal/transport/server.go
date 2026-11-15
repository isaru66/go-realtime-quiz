package transport

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/google/uuid"
	"github.com/isaru66/go-realtime-quiz/backend/internal/config"
	"github.com/isaru66/go-realtime-quiz/backend/internal/game"
	repository "github.com/isaru66/go-realtime-quiz/backend/internal/redis"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	goredis "github.com/redis/go-redis/v9"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

type Options struct {
	Origins        []string
	Admin          string
	Secure         bool
	SchemaPath     string
	QuizPath       string
	TrustedProxies []string
}
type Server struct {
	App           *fiber.App
	Store         *repository.Store
	Engine        *game.Engine
	Catalog       *config.Manager
	Log           *slog.Logger
	Options       Options
	Schema        *jsonschema.Schema
	Context       context.Context
	JoinRate      int
	Draining      atomic.Bool
	socketCount   atomic.Int64
	connections   prometheus.Gauge
	reconnects    *prometheus.CounterVec
	messages      *prometheus.CounterVec
	rejections    *prometheus.CounterVec
	answerLatency prometheus.Histogram
	activeGames   prometheus.Gauge
	activePlayers prometheus.Gauge
	rooms         rooms
}
type apiError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable"`
	RetryAfter int    `json:"retry_after_ms,omitempty"`
}

func errorBody(err error) apiError {
	code := "temporarily_unavailable"
	var domain *game.Error
	if errors.As(err, &domain) {
		code = domain.Code
	}
	b := apiError{Code: code, Message: strings.ReplaceAll(code, "_", " "), Retryable: code == "temporarily_unavailable" || code == "rate_limited"}
	if code == "rate_limited" {
		b.RetryAfter = 1000
	}
	return b
}
func (s *Server) httpError(c *fiber.Ctx, err error) error {
	body := errorBody(err)
	s.rejections.WithLabelValues(body.Code).Inc()
	status := fiber.StatusConflict
	switch body.Code {
	case "invalid_message":
		status = 400
	case "unauthenticated", "invalid_credentials":
		status = 401
	case "forbidden":
		status = 403
	case "quiz_not_found", "wrong_pin":
		status = 404
	case "rate_limited":
		status = 429
		c.Set("Retry-After", "1")
	case "temporarily_unavailable", "capacity_exceeded":
		status = 503
	}
	if body.Code == "temporarily_unavailable" {
		s.Log.Error("HTTP dependency failure", "request_id", c.Locals("request_id"), "error", err)
	}
	return c.Status(status).JSON(fiber.Map{"request_id": c.Locals("request_id"), "error": body})
}

func New(ctx context.Context, store *repository.Store, catalog *config.Manager, options Options, log *slog.Logger) (*Server, error) {
	joinRate := 30
	if value := os.Getenv("JOIN_RATE_PER_MINUTE"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 100000 {
			return nil, fmt.Errorf("JOIN_RATE_PER_MINUTE must be between 1 and 100000")
		}
		joinRate = n
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat = true
	f, err := os.Open(options.SchemaPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err = compiler.AddResource("protocol.json", f); err != nil {
		return nil, err
	}
	schema, err := compiler.Compile("protocol.json#/$defs/client")
	if err != nil {
		return nil, err
	}
	s := &Server{Store: store, Engine: &game.Engine{Repo: store}, Catalog: catalog, Options: options, Log: log, Schema: schema, Context: ctx, JoinRate: joinRate, rooms: rooms{items: map[string]*room{}}}
	registry := prometheus.NewRegistry()
	s.connections = prometheus.NewGauge(prometheus.GaugeOpts{Name: "quiz_websocket_connections", Help: "Local live WebSocket connections."})
	s.reconnects = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "quiz_reconnections_total", Help: "Recovery attempts by result."}, []string{"result"})
	s.messages = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "quiz_room_messages_total", Help: "Room notifications and reconciliations."}, []string{"kind"})
	s.rejections = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "quiz_rejected_commands_total", Help: "Rejected HTTP and WebSocket commands by bounded error code."}, []string{"code"})
	s.answerLatency = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "quiz_answer_submission_seconds", Help: "Answer command latency.", Buckets: prometheus.DefBuckets})
	s.activeGames = prometheus.NewGauge(prometheus.GaugeOpts{Name: "quiz_active_games", Help: "Shared active games, sampled from Redis. Do not sum across pods."})
	s.activePlayers = prometheus.NewGauge(prometheus.GaugeOpts{Name: "quiz_active_players", Help: "Shared active memberships, sampled from Redis. Do not sum across pods."})
	redisLatency := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "quiz_redis_operation_seconds", Help: "Redis operation latency."}, []string{"operation"})
	reloadValid := prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "quiz_configuration_valid", Help: "Last configuration reload valid."}, func() float64 {
		_, status, _, _ := catalog.Get()
		if status == "valid" {
			return 1
		}
		return 0
	})
	registry.MustRegister(s.connections, s.reconnects, s.messages, s.rejections, s.answerLatency, s.activeGames, s.activePlayers, redisLatency, reloadValid, prometheus.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	store.Observe = func(op string, d time.Duration) { redisLatency.WithLabelValues(op).Observe(d.Seconds()) }
	app := fiber.New(fiber.Config{Immutable: true, DisableStartupMessage: true, BodyLimit: 64 * 1024, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, EnableTrustedProxyCheck: true, TrustedProxies: options.TrustedProxies, ProxyHeader: "X-Forwarded-For", EnableIPValidation: true})
	s.App = app
	app.Use(func(c *fiber.Ctx) error {
		id := c.Get("X-Request-ID")
		if _, err := uuid.Parse(id); err != nil {
			id = uuid.NewString()
		}
		c.Locals("request_id", id)
		c.Set("X-Request-ID", id)
		c.Set("Cache-Control", "no-store")
		c.Set("X-Content-Type-Options", "nosniff")
		start := time.Now()
		err := c.Next()
		if c.Path() != "/healthz" && c.Path() != "/livez" && c.Path() != "/readyz" && c.Path() != "/metrics" {
			log.Info("request", "request_id", id, "method", c.Method(), "path", c.Path(), "status", c.Response().StatusCode(), "elapsed_ms", time.Since(start).Milliseconds())
		}
		return err
	})
	app.Get("/healthz", func(c *fiber.Ctx) error { return c.SendString("ok") })
	app.Get("/livez", func(c *fiber.Ctx) error { return c.SendString("ok") })
	app.Get("/readyz", func(c *fiber.Ctx) error {
		if s.Draining.Load() {
			return c.SendStatus(503)
		}
		cat, _, _, _ := catalog.Get()
		if cat == nil {
			return c.SendStatus(503)
		}
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := store.Client.ForEachMaster(ctx, func(ctx context.Context, c *goredis.Client) error {
			info, err := c.ClusterInfo(ctx).Result()
			if err != nil {
				return err
			}
			if !strings.Contains(info, "cluster_state:ok") {
				return fmt.Errorf("Redis Cluster is not ready")
			}
			return nil
		}); err != nil {
			return c.SendStatus(503)
		}
		return c.SendString("ready")
	})
	app.Get("/metrics", adaptor.HTTPHandler(promhttp.HandlerFor(registry, promhttp.HandlerOpts{})))
	app.Get("/api/v1/quizzes", func(c *fiber.Ctx) error {
		cat, _, _, _ := catalog.Get()
		if cat == nil {
			return s.httpError(c, game.Fail("temporarily_unavailable"))
		}
		return c.JSON(fiber.Map{"catalog_version": cat.Version, "quizzes": cat.Metadata()})
	})
	app.Post("/api/v1/auth/session", s.login)
	app.Delete("/api/v1/auth/session", func(c *fiber.Ctx) error {
		if _, err := s.authorize(c, true); err != nil {
			return s.httpError(c, err)
		}
		if err := store.Client.Del(ctx, s.authKey(c.Cookies(s.cookieName()))).Err(); err != nil {
			return s.httpError(c, err)
		}
		c.Cookie(&fiber.Cookie{Name: s.cookieName(), Value: "", Path: "/", HTTPOnly: true, Secure: options.Secure, SameSite: "Strict", Expires: time.Unix(1, 0), MaxAge: -1})
		return c.SendStatus(204)
	})
	app.Get("/api/v1/admin/config", func(c *fiber.Ctx) error {
		if _, err := s.authorize(c, false); err != nil {
			return s.httpError(c, err)
		}
		cat, status, at, validationErrors := catalog.Get()
		var meta []config.Metadata
		if cat != nil {
			meta = cat.Metadata()
		}
		return c.JSON(fiber.Map{"pod_id": os.Getenv("HOSTNAME"), "active_version": cat.Version, "validation_status": status, "last_attempt_at": at, "errors": validationErrors, "quizzes": meta})
	})
	app.Post("/api/v1/games", func(c *fiber.Ctx) error {
		if _, err := s.authorize(c, true); err != nil {
			return s.httpError(c, err)
		}
		if err := store.Limit(ctx, "host", "admin", 60); err != nil {
			return s.httpError(c, err)
		}
		var body struct {
			QuizID  string `json:"quiz_id"`
			Version string `json:"quiz_version"`
		}
		if err := strict(c.Body(), &body); err != nil {
			return s.httpError(c, game.Fail("invalid_message"))
		}
		key := c.Get("Idempotency-Key")
		parsed, err := uuid.Parse(key)
		if err != nil || parsed.Version() != 4 || body.QuizID == "" || len(body.Version) != 64 {
			return s.httpError(c, game.Fail("invalid_message"))
		}
		cat, _, _, _ := catalog.Get()
		created, err := store.Create(ctx, key, body.QuizID, body.Version, cat)
		if err != nil {
			return s.httpError(c, err)
		}
		log.Info("game created or recovered", "game_pin", created.PIN, "session_id", created.ID, "request_id", c.Locals("request_id"))
		return c.Status(201).JSON(created)
	})
	app.Use("/ws", func(c *fiber.Ctx) error {
		if s.Draining.Load() {
			return c.SendStatus(503)
		}
		if !s.allowed(c.Get("Origin")) {
			return c.SendStatus(403)
		}
		if !strings.Contains(","+strings.ReplaceAll(c.Get("Sec-WebSocket-Protocol"), " ", "")+",", ",quiz.v1,") {
			return c.SendStatus(426)
		}
		if !websocket.IsWebSocketUpgrade(c) {
			return c.SendStatus(426)
		}
		c.Locals("admin_token", c.Cookies(s.cookieName()))
		c.Locals("client_ip", c.IP())
		return c.Next()
	})
	app.Get("/ws", websocket.New(s.socket, websocket.Config{Subprotocols: []string{"quiz.v1"}, HandshakeTimeout: 5 * time.Second}))
	return s, nil
}

func (s *Server) allowed(origin string) bool {
	for _, o := range s.Options.Origins {
		if o == origin && o != "" {
			return true
		}
	}
	return false
}
func (s *Server) cookieName() string {
	if s.Options.Secure {
		return "__Host-quiz_admin"
	}
	return "quiz_admin"
}

type adminSession struct {
	CSRF string `json:"csrf"`
}

func (s *Server) authKey(token string) string {
	digest := game.Digest(token)
	return repository.Partition(int(digest[0])%64) + "auth:" + digest
}
func (s *Server) admin(token, csrf string, requireCSRF bool) error {
	if !validToken(token) {
		return game.Fail("unauthenticated")
	}
	ctx, cancel := context.WithTimeout(s.Context, 2*time.Second)
	defer cancel()
	raw, err := s.Store.Client.Get(ctx, s.authKey(token)).Result()
	if err == goredis.Nil {
		return game.Fail("unauthenticated")
	}
	if err != nil {
		return err
	}
	var session adminSession
	if err = json.Unmarshal([]byte(raw), &session); err != nil {
		return err
	}
	if requireCSRF && !game.EqualDigest(session.CSRF, game.Digest(csrf)) {
		return game.Fail("forbidden")
	}
	return nil
}
func (s *Server) authorize(c *fiber.Ctx, mutation bool) (string, error) {
	if mutation && !s.allowed(c.Get("Origin")) {
		return "", game.Fail("forbidden")
	}
	token := c.Cookies(s.cookieName())
	return token, s.admin(token, c.Get("X-CSRF-Token"), mutation)
}
func (s *Server) login(c *fiber.Ctx) error {
	if !s.allowed(c.Get("Origin")) {
		return s.httpError(c, game.Fail("forbidden"))
	}
	if err := s.Store.Limit(s.Context, "login", c.IP(), 10); err != nil {
		return s.httpError(c, err)
	}
	var input struct {
		Credential string `json:"credential"`
	}
	if err := strict(c.Body(), &input); err != nil {
		return s.httpError(c, game.Fail("invalid_message"))
	}
	a, b := game.Digest(input.Credential), game.Digest(s.Options.Admin)
	if subtle.ConstantTimeCompare([]byte(a), []byte(b)) != 1 {
		return s.httpError(c, game.Fail("unauthenticated"))
	}
	token, csrf := repository.Token(), repository.Token()
	data, _ := json.Marshal(adminSession{game.Digest(csrf)})
	if err := s.Store.Client.Set(s.Context, s.authKey(token), data, time.Hour).Err(); err != nil {
		return s.httpError(c, err)
	}
	expires := time.Now().Add(time.Hour)
	c.Cookie(&fiber.Cookie{Name: s.cookieName(), Value: token, Path: "/", HTTPOnly: true, Secure: s.Options.Secure, SameSite: "Strict", Expires: expires, MaxAge: 3600})
	return c.JSON(fiber.Map{"csrf_token": csrf, "expires_at": game.Time(expires.UnixMilli())})
}

func validToken(value string) bool {
	b, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == value
}
func validName(value string) bool {
	if value != strings.TrimSpace(value) || utf8.RuneCountInString(value) < 1 || utf8.RuneCountInString(value) > 40 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return utf8.ValidString(value)
}
func strict(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	if err := uniqueJSON(d); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(target)
}
func uniqueJSON(d *json.Decoder) error {
	t, err := d.Token()
	if err != nil {
		return err
	}
	if delim, ok := t.(json.Delim); ok {
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return fmt.Errorf("duplicate JSON key")
				}
				seen[name] = true
				if err = uniqueJSON(d); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := uniqueJSON(d); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("invalid JSON delimiter")
		}
		_, err = d.Token()
	}
	return err
}

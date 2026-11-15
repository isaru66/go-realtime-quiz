package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/isaru66/go-realtime-quiz/backend/internal/config"
	"github.com/isaru66/go-realtime-quiz/backend/internal/game"
	repository "github.com/isaru66/go-realtime-quiz/backend/internal/redis"
	"github.com/isaru66/go-realtime-quiz/backend/internal/transport"
)

func env(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("backend stopped", "error", err)
		os.Exit(1)
	}
}
func run(log *slog.Logger) error {
	if len(os.Args) == 2 && os.Args[1] == "-healthcheck" {
		_, port, err := net.SplitHostPort(env("LISTEN_ADDR", ":8080"))
		if err != nil {
			return err
		}
		client := &http.Client{Timeout: 2 * time.Second}
		response, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/readyz")
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return fmt.Errorf("readiness returned %d", response.StatusCode)
		}
		return nil
	}
	quizPath := env("QUIZ_PATH", "../../config/quizzes.yaml")
	catalog := &config.Manager{}
	if err := catalog.Reload(quizPath, game.Time(time.Now().UnixMilli())); err != nil {
		return err
	}
	if len(os.Getenv("ADMIN_CREDENTIAL")) < 20 {
		return errors.New("ADMIN_CREDENTIAL must contain at least 20 characters")
	}
	store, err := repository.New(env("REDIS_ADDRS", "localhost:6379"), os.Getenv("REDIS_PASSWORD"), os.Getenv("APP_SECRET"), os.Getenv("REDIS_TLS") == "true", os.Getenv("REDIS_CA_FILE"), log)
	if err != nil {
		return err
	}
	defer store.Client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := transport.New(ctx, store, catalog, transport.Options{Origins: strings.Split(env("ALLOWED_ORIGINS", "http://localhost:5173,http://localhost:8088"), ","), Admin: os.Getenv("ADMIN_CREDENTIAL"), Secure: env("COOKIE_SECURE", "true") != "false", SchemaPath: env("PROTOCOL_SCHEMA_PATH", "../../docs/protocol-v1.schema.json"), QuizPath: quizPath, TrustedProxies: strings.FieldsFunc(os.Getenv("TRUSTED_PROXIES"), func(r rune) bool { return r == ',' })}, log)
	if err != nil {
		return err
	}
	server.Workers(ctx)
	exit := make(chan error, 1)
	go func() {
		log.Info("backend listening", "address", env("LISTEN_ADDR", ":8080"))
		exit <- server.App.Listen(env("LISTEN_ADDR", ":8080"))
	}()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	select {
	case err := <-exit:
		return err
	case <-signals:
		server.Draining.Store(true)
		cancel()
		shutdown, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		log.Info("draining HTTP and WebSocket connections")
		return server.App.ShutdownWithContext(shutdown)
	}
}

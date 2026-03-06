package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/emsbt/url-shortener/internal/handler"
	"github.com/emsbt/url-shortener/internal/middleware"
	"github.com/emsbt/url-shortener/internal/repository"
	"github.com/emsbt/url-shortener/internal/service"
	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
)

func main() {
	// ---- Configuration ----
	port := getEnv("PORT", "8080")
	baseURL := getEnv("BASE_URL", "http://localhost:8080")
	apiKey := getEnv("API_KEY", "default-api-key")
	dbPath := getEnv("DB_PATH", "./data/urls.db")
	logLevel := getEnv("LOG_LEVEL", "info")

	// ---- Logger ----
	logger := buildLogger(logLevel)

	// ---- Repository ----
	// Ensure the data directory exists
	if err := os.MkdirAll("./data", 0o755); err != nil {
		logger.Error("failed to create data directory", slog.String("error", err.Error()))
		os.Exit(1)
	}

	repo, err := repository.NewSQLiteRepository(dbPath)
	if err != nil {
		logger.Error("failed to open database", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// ---- Service ----
	svc := service.NewURLService(repo, baseURL, logger)

	// ---- Handler ----
	h := handler.NewURLHandler(svc, logger)

	// ---- Router ----
	r := chi.NewRouter()

	// Global middleware
	r.Use(chiMiddleware.RealIP)
	r.Use(middleware.Recoverer(logger))
	r.Use(middleware.RequestLogger(logger))

	// Public redirect endpoint (no auth)
	r.Get("/{id}", h.RedirectURL)

	// API routes (protected by API key)
	r.Group(func(r chi.Router) {
		r.Use(middleware.APIKeyAuth(apiKey))
		r.Post("/v1/urls", h.CreateURL)
		r.Get("/v1/urls", h.ListURLs)
		r.Get("/v1/urls/{id}", h.GetURL)
	})

	// ---- Server ----
	addr := fmt.Sprintf(":%s", port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info("server starting", slog.String("addr", addr), slog.String("base_url", baseURL))
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}()

	<-quit
	logger.Info("shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("server shutdown error", slog.String("error", err.Error()))
	}

	logger.Info("server stopped")
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func buildLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}

	// Use JSON in production (non-TTY)
	var h slog.Handler
	if isTerminal() {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}

	return slog.New(h)
}

func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

package main

import (
	"context"
	"embed"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cpamp-viewer/server/internal/config"
	"cpamp-viewer/server/internal/httpapi"
)

//go:embed webdist
var webAssets embed.FS

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration failed", "error", err)
		os.Exit(1)
	}
	app, err := httpapi.New(cfg, webAssets, logger)
	if err != nil {
		logger.Error("initialize server", "error", err)
		os.Exit(1)
	}
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       35 * time.Second,
		WriteTimeout:      35 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	logger.Info("CPAMP Viewer started", "addr", cfg.HTTPAddr, "upstream", safeUpstream(cfg.CPAMPBaseURL), "sub2api_enabled", cfg.Sub2APIBaseURL != "", "public_access", cfg.PublicAccess)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func safeUpstream(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "configured"
	}
	return parsed.Scheme + "://" + parsed.Host
}

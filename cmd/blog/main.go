package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jkjoy/go-blog/internal/blog"
)

var version = "dev"

func main() {
	cfg := blog.ConfigFromEnv()
	flag.StringVar(&cfg.Address, "listen", cfg.Address, "HTTP listen address")
	flag.StringVar(&cfg.DatabaseDriver, "database-driver", cfg.DatabaseDriver, "sqlite, postgres, or mysql")
	flag.StringVar(&cfg.DatabaseDSN, "database", cfg.DatabaseDSN, "database file or DSN")
	flag.StringVar(&cfg.SchemaMode, "schema-mode", cfg.SchemaMode, "validate or init")
	flag.StringVar(&cfg.UploadDir, "uploads", cfg.UploadDir, "upload directory")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel()}))
	application, err := blog.New(cfg, logger, version)
	if err != nil {
		logger.Error("start failed", "error", err)
		os.Exit(1)
	}
	defer application.Close()
	application.StartNotifications()

	server := &http.Server{
		Addr:              cfg.Address,
		Handler:           application.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		logger.Info("blog is ready", "url", "http://"+cfg.Address, "version", version, "database", cfg.DatabaseDriver)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}

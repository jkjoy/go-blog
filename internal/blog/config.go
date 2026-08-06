package blog

import (
	"log/slog"
	"os"
	"strings"
)

type Config struct {
	Address        string
	DatabaseDriver string
	DatabaseDSN    string
	SchemaMode     string
	UploadDir      string
	SessionSecret  string
	TrustedProxy   string
	LogLevelName   string
	SecureCookies  bool
}

func ConfigFromEnv() Config {
	return Config{
		Address:        env("BLOG_LISTEN", "127.0.0.1:8080"),
		DatabaseDriver: strings.ToLower(env("BLOG_DATABASE_DRIVER", "sqlite")),
		DatabaseDSN:    env("BLOG_DATABASE_DSN", "data/blog.sqlite"),
		SchemaMode:     strings.ToLower(env("BLOG_SCHEMA_MODE", "validate")),
		UploadDir:      env("BLOG_UPLOAD_DIR", "uploads"),
		SessionSecret:  os.Getenv("BLOG_SESSION_SECRET"),
		TrustedProxy:   os.Getenv("BLOG_TRUSTED_PROXY"),
		LogLevelName:   strings.ToLower(env("BLOG_LOG_LEVEL", "info")),
		SecureCookies:  strings.EqualFold(os.Getenv("BLOG_SECURE_COOKIES"), "true"),
	}
}

func (c Config) LogLevel() slog.Level {
	switch c.LogLevelName {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

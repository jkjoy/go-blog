package blog

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type Database struct {
	DB      *sql.DB
	Dialect string
}

func OpenDatabase(cfg Config, logger *slog.Logger) (*Database, error) {
	dialect := strings.ToLower(strings.TrimSpace(cfg.DatabaseDriver))
	driver := dialect
	dsn := cfg.DatabaseDSN
	isNew := false

	switch dialect {
	case "sqlite", "sqlite3":
		dialect = "sqlite"
		driver = "sqlite"
		if dsn == "" {
			dsn = "data/blog.sqlite"
		}
		if !strings.HasPrefix(dsn, "file:") && dsn != ":memory:" {
			if _, err := os.Stat(dsn); errors.Is(err, os.ErrNotExist) {
				isNew = true
			}
			if err := os.MkdirAll(filepath.Dir(dsn), 0o755); err != nil {
				return nil, fmt.Errorf("create database directory: %w", err)
			}
		}
	case "postgres", "postgresql", "pgx":
		dialect = "postgres"
		driver = "pgx"
	case "mysql", "mariadb":
		dialect = "mysql"
		driver = "mysql"
	default:
		return nil, fmt.Errorf("unsupported database driver %q", cfg.DatabaseDriver)
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if dialect == "sqlite" {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	} else {
		db.SetMaxOpenConns(20)
		db.SetMaxIdleConns(5)
		db.SetConnMaxIdleTime(5 * time.Minute)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect database: %w", err)
	}

	result := &Database{DB: db, Dialect: dialect}
	if dialect == "sqlite" {
		if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
			db.Close()
			return nil, fmt.Errorf("set sqlite busy timeout: %w", err)
		}
		if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
			db.Close()
			return nil, fmt.Errorf("enable sqlite foreign keys: %w", err)
		}
	}

	if isNew {
		if _, err := db.ExecContext(ctx, sqliteSchema); err != nil {
			db.Close()
			return nil, fmt.Errorf("initialize sqlite database: %w", err)
		}
		logger.Info("initialized new sqlite database", "path", dsn)
	} else if cfg.SchemaMode == "init" && dialect != "sqlite" {
		schema := postgresSchema
		if dialect == "mysql" {
			schema = mysqlSchema
		}
		if err := execSchema(ctx, db, schema); err != nil {
			db.Close()
			return nil, fmt.Errorf("initialize %s database: %w", dialect, err)
		}
		logger.Info("initialized database schema", "dialect", dialect)
	}
	if err := result.Migrate(ctx, logger); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	if err := result.Validate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return result, nil
}

func execSchema(ctx context.Context, db *sql.DB, schema string) error {
	for _, statement := range strings.Split(schema, ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, statement); err != nil {
			if strings.HasPrefix(strings.ToUpper(statement), "CREATE INDEX") && strings.Contains(strings.ToLower(err.Error()), "duplicate") {
				continue
			}
			return err
		}
	}
	return nil
}

func (d *Database) Validate(ctx context.Context) error {
	for _, table := range requiredTables {
		query := "SELECT 1 FROM " + table + " LIMIT 1"
		var value int
		err := d.DB.QueryRowContext(ctx, query).Scan(&value)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("database schema is missing or incompatible at table %s: %w", table, err)
		}
	}
	return nil
}

func (d *Database) Query(query string, args ...any) (*sql.Rows, error) {
	return d.DB.Query(d.Bind(query), args...)
}

func (d *Database) QueryRow(query string, args ...any) *sql.Row {
	return d.DB.QueryRow(d.Bind(query), args...)
}

func (d *Database) Exec(query string, args ...any) (sql.Result, error) {
	return d.DB.Exec(d.Bind(query), args...)
}

func (d *Database) Bind(query string) string {
	if d.Dialect != "postgres" {
		return query
	}
	var b strings.Builder
	index := 1
	for _, char := range query {
		if char == '?' {
			fmt.Fprintf(&b, "$%d", index)
			index++
		} else {
			b.WriteRune(char)
		}
	}
	return b.String()
}

func (d *Database) EnsureInitialData(logger *slog.Logger) error {
	defaults := map[string]string{
		"site_name": "Go Blog", "site_tagline": "记录技术与生活",
		"site_description": "一个使用 Go 构建的轻量博客", "site_footer": "© {year} Go Blog",
		"posts_per_page": "6", "comments_enabled": "1", "comments_notify": "0", "comments_require_approval": "1",
		"pretty_url": "1", "active_theme": "nebula",
	}
	for name, value := range defaults {
		var count int
		if err := d.QueryRow("SELECT COUNT(*) FROM settings WHERE name = ?", name).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := d.Exec("INSERT INTO settings(name,value) VALUES(?,?)", name, value); err != nil {
				return err
			}
		}
	}
	aiDefaults := map[string]string{
		"ai_api_url": "", "ai_api_key": "", "ai_model": "",
		"ai_summary_prompt": defaultAISummaryPrompt, "ai_slug_prompt": defaultAISlugPrompt,
		"ai_polish_prompt": defaultAIPolishPrompt,
	}
	for name, value := range aiDefaults {
		var count int
		if err := d.QueryRow("SELECT COUNT(*) FROM ai_settings WHERE name = ?", name).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := d.Exec("INSERT INTO ai_settings(name,value) VALUES(?,?)", name, value); err != nil {
				return err
			}
		}
	}

	return nil
}

func randomToken(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

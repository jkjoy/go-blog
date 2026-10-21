package blog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log/slog"
	"time"
)

type schemaMigration struct {
	version int64
	apply   func(context.Context, *sql.Conn, *Database) error
}

var schemaMigrations = []schemaMigration{
	{version: 1, apply: func(context.Context, *sql.Conn, *Database) error { return nil }},
	{version: 2, apply: migrateSiteIdentity},
	{version: 3, apply: migrateNotificationJobs},
}

func (d *Database) Migrate(ctx context.Context, logger *slog.Logger) error {
	conn, err := d.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	unlock, err := d.lockMigrations(ctx, conn)
	if err != nil {
		return err
	}
	defer unlock()
	// MySQL DDL commits implicitly; its migrations must be safe to resume.
	// SQLite and PostgreSQL additionally apply the whole upgrade atomically.
	transactional := d.Dialect != "mysql"
	committed := false
	if transactional {
		if d.Dialect != "sqlite" {
			if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
				return err
			}
		}
		defer func() {
			if committed {
				return
			}
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := conn.ExecContext(cleanup, "ROLLBACK"); err != nil {
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			}
		}()
	}
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(
version BIGINT PRIMARY KEY, applied_at BIGINT NOT NULL)`); err != nil {
		return err
	}
	rows, err := conn.QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return err
	}
	applied := 0
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return err
		}
		if applied >= len(schemaMigrations) || schemaMigrations[applied].version != version {
			rows.Close()
			return fmt.Errorf("unsupported or incomplete database migration history at version %d; use a compatible application version", version)
		}
		applied++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, migration := range schemaMigrations[applied:] {
		if err := migration.apply(ctx, conn, d); err != nil {
			return fmt.Errorf("apply version %d: %w", migration.version, err)
		}
		if _, err := conn.ExecContext(ctx, d.Bind("INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)"), migration.version, time.Now().Unix()); err != nil {
			return err
		}
	}
	if transactional {
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return err
		}
		committed = true
	}
	for _, migration := range schemaMigrations[applied:] {
		logger.Info("applied database migration", "version", migration.version)
	}
	return nil
}

func (d *Database) lockMigrations(ctx context.Context, conn *sql.Conn) (func(), error) {
	if d.Dialect == "sqlite" {
		_, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE")
		return func() {}, err
	}
	var releaseQuery string
	var args []any
	if d.Dialect == "postgres" {
		const key int64 = 726146221306128
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
			return nil, err
		}
		releaseQuery, args = "SELECT pg_advisory_unlock($1)", []any{key}
	} else {
		var database string
		if err := conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&database); err != nil {
			return nil, err
		}
		name := fmt.Sprintf("go-blog-migrate-%x", sha256.Sum256([]byte(database)))[:60]
		var acquired sql.NullInt64
		if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 10)", name).Scan(&acquired); err != nil {
			return nil, err
		}
		if !acquired.Valid || acquired.Int64 != 1 {
			return nil, fmt.Errorf("database migration lock unavailable")
		}
		releaseQuery, args = "SELECT RELEASE_LOCK(?)", []any{name}
	}
	return func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(cleanup, releaseQuery, args...); err != nil {
			// Never return a connection with a held advisory lock to the pool.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}, nil
}

func migrateSiteIdentity(ctx context.Context, conn *sql.Conn, d *Database) error {
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS site_identity(id BIGINT PRIMARY KEY, creator_id BIGINT NULL)`); err != nil {
		return err
	}
	query := `INSERT INTO site_identity(id,creator_id) SELECT 1,MIN(id) FROM users WHERE 1=1 ON CONFLICT(id) DO NOTHING`
	if d.Dialect == "mysql" {
		query = `INSERT INTO site_identity(id,creator_id) SELECT 1,MIN(id) FROM users ON DUPLICATE KEY UPDATE id=site_identity.id`
	}
	_, err := conn.ExecContext(ctx, query)
	return err
}

func migrateNotificationJobs(ctx context.Context, conn *sql.Conn, d *Database) error {
	_, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS notification_jobs(
comment_id BIGINT NOT NULL, kind VARCHAR(16) NOT NULL, public_url TEXT NOT NULL, admin_url TEXT NOT NULL,
attempts INTEGER NOT NULL DEFAULT 0, next_attempt_at BIGINT NOT NULL DEFAULT 0,
claim_token VARCHAR(64) NOT NULL DEFAULT '', claimed_until BIGINT NOT NULL DEFAULT 0,
completed_at BIGINT NOT NULL DEFAULT 0, failed_at BIGINT NOT NULL DEFAULT 0,
last_error VARCHAR(500) NOT NULL DEFAULT '', created_at BIGINT NOT NULL,
PRIMARY KEY(comment_id,kind), FOREIGN KEY(comment_id) REFERENCES comments(id) ON DELETE CASCADE)`)
	if err != nil {
		return err
	}
	if d.Dialect == "mysql" {
		var count int
		if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='notification_jobs' AND index_name='idx_notification_jobs_due'`).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		_, err = conn.ExecContext(ctx, `CREATE INDEX idx_notification_jobs_due ON notification_jobs(completed_at,failed_at,next_attempt_at,claimed_until)`)
	} else {
		_, err = conn.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_notification_jobs_due ON notification_jobs(completed_at,failed_at,next_attempt_at,claimed_until)`)
	}
	return err
}

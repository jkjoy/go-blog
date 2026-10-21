package blog

import (
	"context"
	"sync"
	"testing"
)

func prepareBaselineDatabase(t *testing.T, app *App) {
	t.Helper()
	for _, query := range []string{"DROP TABLE notification_jobs", "DROP TABLE site_identity", "DELETE FROM schema_migrations WHERE version>1"} {
		if _, err := app.database.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrationsUpgradeExistingUsersAndAreIdempotent(t *testing.T) {
	app := newTestApp(t)
	owner, err := app.repo.UserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	prepareBaselineDatabase(t, app)
	for range 2 {
		if err := app.database.Migrate(context.Background(), app.logger); err != nil {
			t.Fatal(err)
		}
	}
	user, err := app.repo.UserByID(owner.ID)
	if err != nil || !user.IsCreator {
		t.Fatalf("upgrade lost owner: user=%#v err=%v", user, err)
	}
	var count int
	if err := app.database.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil || count != 3 {
		t.Fatalf("migration count=%d err=%v", count, err)
	}
	if err := app.database.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationsRejectFutureVersionsAndGaps(t *testing.T) {
	for _, test := range []struct{ name, query string }{
		{"future", "INSERT INTO schema_migrations(version,applied_at) VALUES(999,1)"},
		{"gap", "DELETE FROM schema_migrations WHERE version=2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := newTestApp(t)
			if _, err := app.database.Exec(test.query); err != nil {
				t.Fatal(err)
			}
			if err := app.database.Migrate(context.Background(), app.logger); err == nil {
				t.Fatal("incompatible migration history was accepted")
			}
			// A rejection must release the SQLite write lock.
			if _, err := app.database.Exec("UPDATE site_identity SET creator_id=creator_id WHERE id=1"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSQLiteMigrationFailureRollsBackSchemaAndVersion(t *testing.T) {
	app := newTestApp(t)
	prepareBaselineDatabase(t, app)
	if _, err := app.database.Exec(`CREATE TRIGGER fail_upgrade BEFORE INSERT ON schema_migrations WHEN NEW.version=3 BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := app.database.Migrate(context.Background(), app.logger); err == nil {
		t.Fatal("injected migration failure was ignored")
	}
	var count int
	if err := app.database.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name IN ('site_identity','notification_jobs')").Scan(&count); err != nil || count != 0 {
		t.Fatalf("schema not rolled back: count=%d err=%v", count, err)
	}
	if _, err := app.database.Exec("DROP TRIGGER fail_upgrade"); err != nil {
		t.Fatal(err)
	}
	if err := app.database.Migrate(context.Background(), app.logger); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
}

func TestConcurrentSQLiteMigrationsUseOneUpgrade(t *testing.T) {
	app := newTestApp(t)
	prepareBaselineDatabase(t, app)
	var wg sync.WaitGroup
	errors := make(chan error, 4)
	start := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			db, err := OpenDatabase(app.cfg, app.logger)
			if err != nil {
				errors <- err
				return
			}
			db.DB.Close()
		}()
	}
	close(start)
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	if err := app.database.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
}

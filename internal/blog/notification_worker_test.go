package blog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func notificationTestApp(t *testing.T) (*App, int64) {
	t.Helper()
	app := newTestApp(t)
	owner, err := app.repo.UserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	id, err := app.repo.SavePost(Post{AuthorID: owner.ID, Slug: "queued-comments", Title: "Queue", Content: "Body", Kind: "post", Status: "published", AllowComments: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.repo.SaveAdminSettings(map[string]string{"comments_notify": "1"}, MailSettings{Enabled: true, Host: "smtp.example.com", Port: 587, Encryption: "starttls", FromEmail: "sender@example.com", NotifyEmail: "owner@example.com"}, S3Settings{}, AISettings{}); err != nil {
		t.Fatal(err)
	}
	return app, id
}

func queuedTestComment(t *testing.T, app *App, postID, parentID int64, pending bool) int64 {
	t.Helper()
	id, err := app.repo.CreateCommentReturningID(Comment{PostID: postID, ParentID: parentID, AuthorName: "Bob", AuthorEmail: "bob@example.com", Content: "Comment body"}, "hash", "192.0.2.1", "test", pending, "https://blog.example/archive/queued-comments", "https://blog.example/admin/comments")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestNotificationQueueRetriesReplyWithoutResendingAdmin(t *testing.T) {
	app, postID := notificationTestApp(t)
	parentID, err := app.repo.CreateCommentReturningID(Comment{PostID: postID, AuthorName: "Alice", AuthorEmail: "alice@example.com", Content: "Parent"}, "hash", "192.0.2.1", "test", false)
	if err != nil {
		t.Fatal(err)
	}
	id := queuedTestComment(t, app, postID, parentID, false)
	adminSends, replySends := 0, 0
	failReply := true
	app.sendMail = func(_ context.Context, _ MailSettings, recipient, _, _ string) error {
		if recipient == "owner@example.com" {
			adminSends++
			return nil
		}
		replySends++
		if failReply {
			return errors.New("simulated SMTP failure")
		}
		return nil
	}
	if err := app.processNotificationJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	jobs, err := app.repo.NotificationJobs()
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.Kind == "admin" && job.CompletedAt == 0 {
			t.Fatal("successful admin notification was not completed")
		}
		if job.Kind == "reply" && (job.CompletedAt != 0 || job.Attempts != 1 || job.NextAttemptAt <= time.Now().Unix() || job.ClaimToken != "") {
			t.Fatalf("failed reply not rescheduled: %#v", job)
		}
	}
	if err := app.processNotificationJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if adminSends != 1 || replySends != 1 {
		t.Fatal("backoff did not prevent immediate retry")
	}
	if _, err := app.database.Exec("UPDATE notification_jobs SET next_attempt_at=0 WHERE comment_id=? AND kind='reply'", id); err != nil {
		t.Fatal(err)
	}
	failReply = false
	if err := app.processNotificationJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if adminSends != 1 || replySends != 2 {
		t.Fatalf("admin=%d reply=%d", adminSends, replySends)
	}
	jobs, err = app.repo.NotificationJobs()
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.CompletedAt == 0 || job.FailedAt != 0 {
			t.Fatalf("job not completed: %#v", job)
		}
	}
}

func TestNotificationQueuePersistsAcrossRestart(t *testing.T) {
	app, postID := notificationTestApp(t)
	queuedTestComment(t, app, postID, 0, false)
	cfg, logger := app.cfg, app.logger
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(cfg, logger, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	sent := make(chan struct{}, 1)
	restarted.sendMail = func(context.Context, MailSettings, string, string, string) error { sent <- struct{}{}; return nil }
	restarted.StartNotifications()
	restarted.StartNotifications() // Starting twice must not spawn another worker.
	select {
	case <-sent:
	case <-time.After(3 * time.Second):
		t.Fatal("persisted job was not recovered at startup")
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
	verified, err := New(cfg, logger, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer verified.Close()
	jobs, err := verified.repo.NotificationJobs()
	if err != nil || len(jobs) != 1 || jobs[0].CompletedAt == 0 {
		t.Fatalf("delivery state not saved: jobs=%#v err=%v", jobs, err)
	}
}

func TestNotificationQueueLimitsAttemptsAndSupportsProtectedRetry(t *testing.T) {
	app, postID := notificationTestApp(t)
	id := queuedTestComment(t, app, postID, 0, false)
	app.sendMail = func(context.Context, MailSettings, string, string, string) error {
		return errors.New("private SMTP response must not appear in UI")
	}
	for range maxNotificationAttempts {
		if _, err := app.database.Exec("UPDATE notification_jobs SET next_attempt_at=0 WHERE comment_id=?", id); err != nil {
			t.Fatal(err)
		}
		if err := app.processNotificationJobs(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := app.repo.NotificationJobs()
	if err != nil || len(jobs) != 1 || jobs[0].FailedAt == 0 || jobs[0].Attempts != maxNotificationAttempts {
		t.Fatalf("retry limit not enforced: jobs=%#v err=%v", jobs, err)
	}
	cookie, _ := loginTestUser(t, app, "admin", "test-password", "")
	request := httptest.NewRequest(http.MethodGet, "/admin/notifications", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "重新发送") || strings.Contains(response.Body.String(), "private SMTP") {
		t.Fatalf("notification status page failed: status=%d body=%s", response.Code, response.Body.String())
	}
	path := "/admin/notifications/" + strconv.FormatInt(id, 10) + "/admin/retry"
	for _, authorized := range []bool{false, true} {
		form := url.Values{}
		if authorized {
			form.Set("csrf", cookieCSRF(t, app, cookie))
		}
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		want := http.StatusForbidden
		if authorized {
			want = http.StatusSeeOther
		}
		if response.Code != want {
			t.Fatalf("retry authorized=%v status=%d body=%s", authorized, response.Code, response.Body.String())
		}
	}
	app.sendMail = func(context.Context, MailSettings, string, string, string) error { return nil }
	if err := app.processNotificationJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	jobs, err = app.repo.NotificationJobs()
	if err != nil || jobs[0].CompletedAt == 0 || jobs[0].Attempts != 1 {
		t.Fatalf("manual retry failed: jobs=%#v err=%v", jobs, err)
	}
}

func TestNotificationQueueCommentAndApprovalAreAtomic(t *testing.T) {
	app, postID := notificationTestApp(t)
	if _, err := app.database.Exec(`CREATE TRIGGER fail_notification BEFORE INSERT ON notification_jobs BEGIN SELECT RAISE(ABORT,'injected queue failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := app.repo.CreateCommentReturningID(Comment{PostID: postID, AuthorName: "Bob", AuthorEmail: "bob@example.com", Content: "Body"}, "hash", "192.0.2.1", "test", false, "https://blog.example/post", "https://blog.example/admin/comments")
	if err == nil {
		t.Fatal("queue insert failure was swallowed")
	}
	var count int
	if err := app.database.QueryRow("SELECT COUNT(*) FROM comments").Scan(&count); err != nil || count != 0 {
		t.Fatalf("comment committed without job: count=%d err=%v", count, err)
	}
	if _, err := app.database.Exec("DROP TRIGGER fail_notification"); err != nil {
		t.Fatal(err)
	}
	parentID, err := app.repo.CreateCommentReturningID(Comment{PostID: postID, AuthorName: "Alice", AuthorEmail: "alice@example.com", Content: "Parent"}, "hash", "192.0.2.1", "test", false)
	if err != nil {
		t.Fatal(err)
	}
	id := queuedTestComment(t, app, postID, parentID, true)
	if _, err := app.database.Exec(`CREATE TRIGGER fail_approval BEFORE INSERT ON notification_jobs WHEN NEW.kind='reply' BEGIN SELECT RAISE(ABORT,'injected approval failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := app.repo.ModerateComment(id, "approve", "https://blog.example/post", "https://blog.example/admin/comments"); err == nil {
		t.Fatal("approval succeeded without durable reply job")
	}
	comment, err := app.repo.CommentNotification(id)
	if err != nil || comment.Status != "pending" {
		t.Fatalf("approval was not rolled back: comment=%#v err=%v", comment, err)
	}
	if _, err := app.database.Exec("DROP TRIGGER fail_approval"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := app.repo.ModerateComment(id, "approve", "https://blog.example/post", "https://blog.example/admin/comments"); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.database.QueryRow("SELECT COUNT(*) FROM notification_jobs WHERE comment_id=?", id).Scan(&count); err != nil || count != 2 {
		t.Fatalf("approval duplicated notification: count=%d err=%v", count, err)
	}
}

func TestConcurrentNotificationWorkersClaimOnce(t *testing.T) {
	app, postID := notificationTestApp(t)
	queuedTestComment(t, app, postID, 0, false)
	var sends atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	app.sendMail = func(context.Context, MailSettings, string, string, string) error {
		sends.Add(1)
		close(entered)
		<-release
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- app.processNotificationJobs(context.Background()) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("sender was not called")
	}
	err := app.processNotificationJobs(context.Background())
	close(release)
	if firstErr := <-done; firstErr != nil {
		t.Fatal(firstErr)
	}
	if err != nil || sends.Load() != 1 {
		t.Fatalf("concurrent queue sent %d times err=%v", sends.Load(), err)
	}
}

func TestNotificationQueueRecoversExpiredLeaseAndPausesWhenDisabled(t *testing.T) {
	app, postID := notificationTestApp(t)
	id := queuedTestComment(t, app, postID, 0, false)
	if _, err := app.database.Exec("UPDATE notification_jobs SET claim_token='crashed',claimed_until=?,attempts=1 WHERE comment_id=?", time.Now().Add(-time.Minute).Unix(), id); err != nil {
		t.Fatal(err)
	}
	var sends int
	app.sendMail = func(context.Context, MailSettings, string, string, string) error { sends++; return nil }
	if err := app.repo.SavePublicSettings(map[string]string{"comments_notify": "0"}); err != nil {
		t.Fatal(err)
	}
	if err := app.processNotificationJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sends != 0 {
		t.Fatal("disabled notifications were sent")
	}
	if err := app.repo.SavePublicSettings(map[string]string{"comments_notify": "1"}); err != nil {
		t.Fatal(err)
	}
	if err := app.processNotificationJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	jobs, err := app.repo.NotificationJobs()
	if err != nil || len(jobs) != 1 || jobs[0].CompletedAt == 0 || sends != 1 {
		t.Fatalf("expired task not recovered: jobs=%#v sends=%d err=%v", jobs, sends, err)
	}
}

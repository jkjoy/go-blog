package blog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	path := filepath.Join(t.TempDir(), "blog.sqlite")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := New(Config{
		Address: "127.0.0.1:0", DatabaseDriver: "sqlite", DatabaseDSN: path,
		UploadDir: t.TempDir(), SessionSecret: strings.Repeat("test-secret-", 4),
	}, logger, "test")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return app
}

func TestFreshDatabasePublicRoutes(t *testing.T) {
	app := newTestApp(t)
	for _, path := range []string{"/", "/archives", "/tags", "/links", "/rss.xml", "/sitemap.xml", "/login", "/healthz"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			response := httptest.NewRecorder()
			app.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, body = %s", path, response.Code, response.Body.String())
			}
		})
	}
}

func TestPublishedPostRoundTrip(t *testing.T) {
	app := newTestApp(t)
	user, err := app.repo.UserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	id, err := app.repo.SavePost(Post{
		AuthorID: user.ID, Slug: "hello-go", Title: "Hello Go", Content: "# 安全内容\n\n<script>alert(1)</script>",
		Kind: "post", Status: "published", AllowComments: true, Tags: []Tag{{Label: "Go"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if id < 1 {
		t.Fatalf("invalid id %d", id)
	}

	request := httptest.NewRequest(http.MethodGet, "/archive/hello-go", nil)
	request.RemoteAddr = "127.0.0.1:10000"
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte("Hello Go")) {
		t.Fatal("rendered page does not contain title")
	}
	if bytes.Contains(response.Body.Bytes(), []byte("<script>alert(1)</script>")) {
		t.Fatal("unsafe script was not sanitized")
	}

	aliasRequest := httptest.NewRequest(http.MethodGet, "/post/hello-go", nil)
	aliasResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(aliasResponse, aliasRequest)
	if aliasResponse.Code != http.StatusNotFound {
		t.Fatalf("unsupported post alias status = %d, body = %s", aliasResponse.Code, aliasResponse.Body.String())
	}
}

func TestUnicodeSlugLinksAndDirectRoute(t *testing.T) {
	app := newTestApp(t)
	user, err := app.repo.UserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	slug := "中文文章"
	if _, err := app.repo.SavePost(Post{AuthorID: user.ID, Slug: slug, Title: "中文文章", Content: "正文", Kind: "post", Status: "published"}); err != nil {
		t.Fatal(err)
	}

	homeRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	homeResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(homeResponse, homeRequest)
	wantPath := "/archive/" + url.PathEscape(slug)
	if !strings.Contains(homeResponse.Body.String(), wantPath) {
		t.Fatalf("home does not contain escaped Unicode URL %q: %s", wantPath, homeResponse.Body.String())
	}

	directRequest := httptest.NewRequest(http.MethodGet, wantPath, nil)
	directResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(directResponse, directRequest)
	if directResponse.Code != http.StatusOK {
		t.Fatalf("Unicode route status = %d, body = %s", directResponse.Code, directResponse.Body.String())
	}

	lowercaseRequest := httptest.NewRequest(http.MethodGet, strings.ToLower(wantPath), nil)
	lowercaseResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(lowercaseResponse, lowercaseRequest)
	if lowercaseResponse.Code != http.StatusOK {
		t.Fatalf("lowercase escaped Unicode route status = %d, body = %s", lowercaseResponse.Code, lowercaseResponse.Body.String())
	}
}

func TestUnicodeSlugNormalizesToCanonicalPath(t *testing.T) {
	app := newTestApp(t)
	user, err := app.repo.UserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.repo.SavePost(Post{AuthorID: user.ID, Slug: "café", Title: "Cafe", Content: "Body", Kind: "post", Status: "published"}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/archive/"+url.PathEscape("cafe\u0301"), nil)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusMovedPermanently {
		t.Fatalf("normalization status = %d, body = %s", response.Code, response.Body.String())
	}
	if location := response.Header().Get("Location"); location != "/archive/"+url.PathEscape("café") {
		t.Fatalf("canonical Location = %q", location)
	}
}

func TestClosedCommentsRemainVisible(t *testing.T) {
	app := newTestApp(t)
	user, err := app.repo.UserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	postID, err := app.repo.SavePost(Post{AuthorID: user.ID, Slug: "closed-comments", Title: "Closed comments", Content: "Body", Kind: "post", Status: "published", AllowComments: false})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.repo.CreateComment(Comment{PostID: postID, AuthorName: "Alice", AuthorEmail: "alice@example.com", Content: "Root comment"}, "hash-a", "127.0.0.1", "test", false); err != nil {
		t.Fatal(err)
	}
	var parentID int64
	if err := app.database.QueryRow("SELECT id FROM comments WHERE post_id=? ORDER BY id LIMIT 1", postID).Scan(&parentID); err != nil {
		t.Fatal(err)
	}
	if err := app.repo.CreateComment(Comment{PostID: postID, ParentID: parentID, ReplyToName: "Alice", AuthorName: "Bob", AuthorEmail: "bob@example.com", Content: "Nested reply"}, "hash-b", "127.0.0.1", "test", false); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/archive/closed-comments", nil)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, body)
	}
	for _, want := range []string{"Root comment", "Nested reply", "2 条评论", "评论已关闭"} {
		if !strings.Contains(body, want) {
			t.Fatalf("comment page does not contain %q: %s", want, body)
		}
	}
	if strings.Contains(body, `<form class="comment-form"`) {
		t.Fatal("closed comment page still contains the comment form")
	}
}

func TestLoginAndAdminAccess(t *testing.T) {
	app := newTestApp(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("test-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.database.Exec("UPDATE users SET password_hash=? WHERE username='admin'", string(hash)); err != nil {
		t.Fatal(err)
	}

	form := url.Values{"username": {"admin"}, "password": {"test-password"}}
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, body = %s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login did not set a session cookie")
	}

	adminRequest := httptest.NewRequest(http.MethodGet, "/admin", nil)
	adminRequest.AddCookie(cookies[0])
	adminResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(adminResponse, adminRequest)
	if adminResponse.Code != http.StatusOK {
		t.Fatalf("admin status = %d, body = %s", adminResponse.Code, adminResponse.Body.String())
	}
	if !strings.Contains(adminResponse.Body.String(), "CONTENT CONSOLE") {
		t.Fatal("admin page was not rendered")
	}
}

func TestAuthenticatedAdminRoutesAndUpload(t *testing.T) {
	app := newTestApp(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("test-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.database.Exec("UPDATE users SET password_hash=? WHERE username='admin'", string(hash)); err != nil {
		t.Fatal(err)
	}

	form := url.Values{"username": {"admin"}, "password": {"test-password"}}
	loginRequest := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	loginRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(loginResponse, loginRequest)
	cookie := loginResponse.Result().Cookies()[0]

	for _, path := range []string{"/admin", "/admin/posts", "/admin/comments", "/admin/categories", "/admin/links", "/admin/settings", "/admin/write"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, body = %s", path, response.Code, response.Body.String())
		}
	}

	claimsRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	claimsRequest.AddCookie(cookie)
	claims, err := app.auth.Claims(claimsRequest)
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("csrf", app.auth.CSRF(claims)); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("hello from go blog\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	uploadRequest := httptest.NewRequest(http.MethodPost, "/admin/upload", &body)
	uploadRequest.Header.Set("Content-Type", writer.FormDataContentType())
	uploadRequest.AddCookie(cookie)
	uploadResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(uploadResponse, uploadRequest)
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	if !strings.Contains(uploadResponse.Body.String(), "/uploads/") {
		t.Fatal("upload response has no public URL")
	}
}

func TestExcerptNormalizesStoredMarkdown(t *testing.T) {
	post := Post{Excerpt: "# Heading\n\n- first\n- [second](https://example.com)\n\n```go\nsecret()\n```"}
	got := DeriveExcerpt(post, 200)
	if strings.ContainsAny(got, "#[]`()") {
		t.Fatalf("excerpt still contains markdown: %q", got)
	}
	if !strings.Contains(got, "Heading") || !strings.Contains(got, "second") {
		t.Fatalf("excerpt lost text: %q", got)
	}
}

func TestExcerptNormalizesFlattenedMarkdown(t *testing.T) {
	post := Post{Excerpt: "# Intro text. ## Next section - first item - second item"}
	got := DeriveExcerpt(post, 200)
	if strings.Contains(got, "#") || strings.Contains(got, " - ") {
		t.Fatalf("flattened excerpt still contains markdown: %q", got)
	}
}

func TestPostgresPlaceholders(t *testing.T) {
	db := &Database{Dialect: "postgres"}
	got := db.Bind("SELECT * FROM posts WHERE id=? AND slug=?")
	if got != "SELECT * FROM posts WHERE id=$1 AND slug=$2" {
		t.Fatalf("Bind() = %q", got)
	}
}

func TestDatabaseSchemaValidation(t *testing.T) {
	app := newTestApp(t)
	if err := app.database.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOnlyFirstUserCanAccessAdmin(t *testing.T) {
	app := newTestApp(t)
	creator, err := app.repo.UserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	if !creator.IsCreator {
		t.Fatal("first user was not marked as creator")
	}
	if _, err := app.repo.SavePost(Post{AuthorID: creator.ID, Slug: "private-draft", Title: "Private draft", Content: "Draft body", Kind: "post", Status: "draft"}); err != nil {
		t.Fatal(err)
	}

	subscriber := addTestUser(t, app, "reader", "reader-password")
	if subscriber.IsCreator {
		t.Fatal("later user was marked as creator")
	}
	cookie, response := loginTestUser(t, app, "reader", "reader-password", "/admin")
	if location := response.Header().Get("Location"); location != "/" {
		t.Fatalf("subscriber login redirected to %q", location)
	}

	request := httptest.NewRequest(http.MethodGet, "/admin", nil)
	request.AddCookie(cookie)
	adminResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(adminResponse, request)
	if adminResponse.Code != http.StatusForbidden {
		t.Fatalf("subscriber admin status = %d, body = %s", adminResponse.Code, adminResponse.Body.String())
	}
	draftRequest := httptest.NewRequest(http.MethodGet, "/archive/private-draft", nil)
	draftRequest.AddCookie(cookie)
	draftResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(draftResponse, draftRequest)
	if draftResponse.Code != http.StatusNotFound {
		t.Fatalf("subscriber draft status = %d, body = %s", draftResponse.Code, draftResponse.Body.String())
	}

	homeRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	homeRequest.AddCookie(cookie)
	homeResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(homeResponse, homeRequest)
	body := homeResponse.Body.String()
	if !strings.Contains(body, `class="nav-logout"`) || strings.Contains(body, `href="/admin">管理`) {
		t.Fatalf("subscriber navigation has incorrect permissions: %s", body)
	}
}

func TestSubscriberCanComment(t *testing.T) {
	app := newTestApp(t)
	creator, err := app.repo.UserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	postID, err := app.repo.SavePost(Post{AuthorID: creator.ID, Slug: "subscriber-comment", Title: "Subscriber", Content: "Body", Kind: "post", Status: "published", AllowComments: true})
	if err != nil {
		t.Fatal(err)
	}
	subscriber := addTestUser(t, app, "commenter", "comment-password")
	cookie, _ := loginTestUser(t, app, subscriber.Username, "comment-password", "")
	form := url.Values{"post_id": {strconv.FormatInt(postID, 10)}, "content": {"Authenticated comment"}}
	request := httptest.NewRequest(http.MethodPost, "/comments", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.RemoteAddr = "192.0.2.20:1234"
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("comment status = %d, body = %s", response.Code, response.Body.String())
	}
	var userID int64
	if err := app.database.QueryRow("SELECT COALESCE(user_id,0) FROM comments WHERE post_id=?", postID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if userID != subscriber.ID {
		t.Fatalf("comment user_id = %d, want %d", userID, subscriber.ID)
	}
	pageRequest := httptest.NewRequest(http.MethodGet, "/archive/subscriber-comment", nil)
	pageRequest.AddCookie(cookie)
	pageResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(pageResponse, pageRequest)
	if strings.Contains(pageResponse.Body.String(), "Authenticated comment") {
		t.Fatal("subscriber can see a pending comment")
	}
}

func TestLoginRateLimit(t *testing.T) {
	app := newTestApp(t)
	for attempt := 1; attempt <= 9; attempt++ {
		form := url.Values{"username": {"admin"}, "password": {"wrong-password"}}
		request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.RemoteAddr = "192.0.2.30:1234"
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		want := http.StatusUnauthorized
		if attempt == 9 {
			want = http.StatusTooManyRequests
		}
		if response.Code != want {
			t.Fatalf("attempt %d status = %d, want %d", attempt, response.Code, want)
		}
	}
}

func TestCommentRateLimit(t *testing.T) {
	app := newTestApp(t)
	creator, err := app.repo.UserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	postID, err := app.repo.SavePost(Post{AuthorID: creator.ID, Slug: "rate-limit", Title: "Rate limit", Content: "Body", Kind: "post", Status: "published", AllowComments: true})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 6; attempt++ {
		form := url.Values{
			"post_id": {strconv.FormatInt(postID, 10)}, "author_name": {"Visitor"},
			"author_email": {"visitor@example.com"}, "content": {"A valid comment"},
		}
		request := httptest.NewRequest(http.MethodPost, "/comments", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.RemoteAddr = "192.0.2.40:1234"
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		want := http.StatusSeeOther
		if attempt == 6 {
			want = http.StatusTooManyRequests
		}
		if response.Code != want {
			t.Fatalf("attempt %d status = %d, want %d, body = %s", attempt, response.Code, want, response.Body.String())
		}
	}
}

func TestTrustedProxyIPExtraction(t *testing.T) {
	e := echo.New()
	if err := configureIPExtractor(e, "10.0.0.0/8"); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.2:1234"
	request.Header.Set(echo.HeaderXForwardedFor, "203.0.113.9")
	if got := clientIP(e.NewContext(request, httptest.NewRecorder())); got != "203.0.113.9" {
		t.Fatalf("trusted proxy client IP = %q", got)
	}

	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "198.51.100.8:1234"
	request.Header.Set(echo.HeaderXForwardedFor, "203.0.113.9")
	if got := clientIP(e.NewContext(request, httptest.NewRecorder())); got != "198.51.100.8" {
		t.Fatalf("untrusted proxy spoofed client IP = %q", got)
	}
	if err := configureIPExtractor(e, "not-an-address"); err == nil {
		t.Fatal("invalid trusted proxy configuration was accepted")
	}
}

func TestReplyStoresParentAuthor(t *testing.T) {
	app := newTestApp(t)
	creator, err := app.repo.UserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	postID, err := app.repo.SavePost(Post{AuthorID: creator.ID, Slug: "reply", Title: "Reply", Content: "Body", Kind: "post", Status: "published", AllowComments: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.repo.CreateComment(Comment{PostID: postID, AuthorName: "Alice", AuthorEmail: "alice@example.com", Content: "Parent"}, "parent", "192.0.2.1", "test", false); err != nil {
		t.Fatal(err)
	}
	var parentID int64
	if err := app.database.QueryRow("SELECT id FROM comments WHERE post_id=?", postID).Scan(&parentID); err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"post_id": {strconv.FormatInt(postID, 10)}, "parent_id": {strconv.FormatInt(parentID, 10)},
		"author_name": {"Bob"}, "author_email": {"bob@example.com"}, "content": {"Reply body"},
	}
	request := httptest.NewRequest(http.MethodPost, "/comments", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.RemoteAddr = "192.0.2.50:1234"
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("reply status = %d, body = %s", response.Code, response.Body.String())
	}
	var replyTo string
	if err := app.database.QueryRow("SELECT reply_to_name FROM comments WHERE parent_id=?", parentID).Scan(&replyTo); err != nil {
		t.Fatal(err)
	}
	if replyTo != "Alice" {
		t.Fatalf("reply_to_name = %q", replyTo)
	}
}

func TestCommentNotificationsSendAdminAndReplyOnce(t *testing.T) {
	app := newTestApp(t)
	creator, err := app.repo.UserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	postID, err := app.repo.SavePost(Post{
		AuthorID: creator.ID, Slug: "notifications", Title: "Notifications", Content: "Body",
		Kind: "post", Status: "published", AllowComments: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.repo.SaveAdminSettings(map[string]string{"comments_notify": "1"}, MailSettings{
		Enabled: true, Encryption: "starttls", Host: "smtp.example.com", Port: 587,
		Username: "sender@example.com", Password: "smtp-secret", FromEmail: "sender@example.com",
		FromName: "Go Blog", NotifyEmail: "owner@example.com",
	}, S3Settings{}, AISettings{}); err != nil {
		t.Fatal(err)
	}
	parentID, err := app.repo.CreateCommentReturningID(Comment{
		PostID: postID, AuthorName: "Alice", AuthorEmail: "alice@example.com", Content: "Parent",
	}, "parent", "192.0.2.1", "test", false)
	if err != nil {
		t.Fatal(err)
	}
	replyID, err := app.repo.CreateCommentReturningID(Comment{
		PostID: postID, ParentID: parentID, ReplyToName: "Alice",
		AuthorName: "Bob", AuthorEmail: "bob@example.com", Content: "Reply",
	}, "reply", "192.0.2.2", "test", false)
	if err != nil {
		t.Fatal(err)
	}
	var recipients []string
	app.sendMail = func(_ context.Context, _ MailSettings, recipient, _, _ string) error {
		recipients = append(recipients, recipient)
		return nil
	}
	if err := app.sendCommentNotifications(context.Background(), replyID, "https://blog.example/archive/notifications", "https://blog.example/admin/comments", true, true); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(recipients, ","); got != "owner@example.com,alice@example.com" {
		t.Fatalf("notification recipients = %q", got)
	}
	if err := app.sendCommentNotifications(context.Background(), replyID, "https://blog.example/archive/notifications", "https://blog.example/admin/comments", false, true); err != nil {
		t.Fatal(err)
	}
	if len(recipients) != 2 {
		t.Fatalf("reply notification was sent more than once: %#v", recipients)
	}
	var notifiedAt int64
	if err := app.database.QueryRow("SELECT reply_notified_at FROM comments WHERE id=?", replyID).Scan(&notifiedAt); err != nil {
		t.Fatal(err)
	}
	if notifiedAt == 0 {
		t.Fatal("reply notification was not marked as sent")
	}
}

func TestS3UploadUsesPublicURLWithoutLocalCopy(t *testing.T) {
	app := newTestApp(t)
	if err := app.repo.SaveAdminSettings(nil, MailSettings{}, S3Settings{
		Enabled: true, Endpoint: "https://s3.example.com", Region: "auto", Bucket: "media",
		AccessKey: "access-key", SecretKey: "secret-key", PublicURL: "https://cdn.example.com",
		PathPrefix: "blog", PathStyle: true, KeepLocal: false,
	}, AISettings{}); err != nil {
		t.Fatal(err)
	}
	var uploadedKey, uploadedType, uploadedBody string
	app.putObject = func(_ context.Context, _ S3Settings, key, contentType string, body io.Reader, _ int64) error {
		data, err := io.ReadAll(body)
		if err != nil {
			return err
		}
		uploadedKey, uploadedType, uploadedBody = key, contentType, string(data)
		return nil
	}
	cookie, _ := loginTestUser(t, app, "admin", resetAdminPassword(t, app), "")
	requestForClaims := httptest.NewRequest(http.MethodGet, "/", nil)
	requestForClaims.AddCookie(cookie)
	claims, err := app.auth.Claims(requestForClaims)
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("csrf", app.auth.CSRF(claims)); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("stored in s3\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/upload", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("S3 upload status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "https://cdn.example.com/blog/") || !strings.HasSuffix(uploadedKey, ".txt") {
		t.Fatalf("unexpected S3 URL or key: body=%s key=%s", response.Body.String(), uploadedKey)
	}
	if uploadedType != "text/plain; charset=utf-8" || uploadedBody != "stored in s3\n" {
		t.Fatalf("uploaded object type=%q body=%q", uploadedType, uploadedBody)
	}
	entries, err := os.ReadDir(app.cfg.UploadDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("S3-only upload wrote local files: %#v", entries)
	}

	settingsRequest := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	settingsRequest.AddCookie(cookie)
	settingsResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(settingsResponse, settingsRequest)
	settingsBody := settingsResponse.Body.String()
	if strings.Contains(settingsBody, "access-key") || strings.Contains(settingsBody, "secret-key") {
		t.Fatal("S3 credentials were rendered in the settings page")
	}
	if !strings.Contains(settingsBody, "已配置，留空保持不变") {
		t.Fatal("settings page does not indicate saved credentials")
	}
}

func TestSettingsSavePreservesStoredSecrets(t *testing.T) {
	app := newTestApp(t)
	mailSettings := MailSettings{
		Enabled: true, Encryption: "ssl", Host: "smtp.example.com", Port: 465,
		Username: "sender@example.com", Password: "smtp-secret", FromEmail: "sender@example.com",
		NotifyEmail: "owner@example.com",
	}
	s3Settings := S3Settings{
		Enabled: true, Endpoint: "https://s3.example.com", Region: "auto", Bucket: "media",
		AccessKey: "access-secret", SecretKey: "storage-secret", PublicURL: "https://cdn.example.com",
		PathPrefix: "uploads", KeepLocal: true,
	}
	aiSettings := AISettings{
		APIURL: "https://api.example.com/v1", APIKey: "ai-secret", Model: "example-model",
		SummaryPrompt: defaultAISummaryPrompt, SlugPrompt: defaultAISlugPrompt, PolishPrompt: defaultAIPolishPrompt,
	}
	if err := app.repo.SaveAdminSettings(nil, mailSettings, s3Settings, aiSettings); err != nil {
		t.Fatal(err)
	}
	cookie, _ := loginTestUser(t, app, "admin", resetAdminPassword(t, app), "")
	claimsRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	claimsRequest.AddCookie(cookie)
	claims, err := app.auth.Claims(claimsRequest)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"csrf": {app.auth.CSRF(claims)}, "site_name": {"Go Blog"}, "posts_per_page": {"6"},
		"comments_enabled": {"1"}, "comments_notify": {"1"}, "comments_require_approval": {"1"},
		"smtp_enabled": {"1"}, "smtp_encryption": {"ssl"}, "smtp_host": {"smtp.example.com"},
		"smtp_port": {"465"}, "smtp_username": {"sender@example.com"},
		"smtp_from_email": {"sender@example.com"}, "smtp_notify_email": {"owner@example.com"},
		"s3_enabled": {"1"}, "s3_endpoint": {"https://s3.example.com"}, "s3_region": {"auto"},
		"s3_bucket": {"media"}, "s3_public_url": {"https://cdn.example.com"},
		"s3_path_prefix": {"uploads"}, "s3_keep_local": {"1"},
		"ai_api_url": {"https://api.example.com/v1"}, "ai_model": {"example-model"},
		"ai_summary_prompt": {defaultAISummaryPrompt}, "ai_slug_prompt": {defaultAISlugPrompt},
		"ai_polish_prompt": {defaultAIPolishPrompt},
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/settings", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("settings save status = %d, body = %s", response.Code, response.Body.String())
	}
	savedMail, err := app.repo.MailSettings()
	if err != nil {
		t.Fatal(err)
	}
	savedS3, err := app.repo.S3Settings()
	if err != nil {
		t.Fatal(err)
	}
	savedAI, err := app.repo.AISettings()
	if err != nil {
		t.Fatal(err)
	}
	if savedMail.Password != "smtp-secret" || savedS3.AccessKey != "access-secret" || savedS3.SecretKey != "storage-secret" || savedAI.APIKey != "ai-secret" {
		t.Fatal("saving settings without new secret values did not preserve stored credentials")
	}
}

func TestAICompletionUsesCompatibleEndpoint(t *testing.T) {
	var requestURL, authorization, model string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestURL, authorization = request.URL.String(), request.Header.Get("Authorization")
		var payload aiCompletionRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			return nil, err
		}
		model = payload.Model
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"完成"}}]}`)),
		}, nil
	})}
	result, err := requestAICompletion(context.Background(), client, AISettings{
		APIURL: "https://api.example.com/v1", APIKey: "secret", Model: "test-model",
	}, "system", "user", 100)
	if err != nil {
		t.Fatal(err)
	}
	if result != "完成" || requestURL != "https://api.example.com/v1/chat/completions" || authorization != "Bearer secret" || model != "test-model" {
		t.Fatalf("unexpected AI request/result: result=%q url=%q auth=%q model=%q", result, requestURL, authorization, model)
	}
}

func TestAIActionsAndSecretRendering(t *testing.T) {
	app := newTestApp(t)
	aiSettings := AISettings{
		APIURL: "https://api.example.com/v1", APIKey: "hidden-ai-key", Model: "test-model",
		SummaryPrompt: defaultAISummaryPrompt, SlugPrompt: defaultAISlugPrompt, PolishPrompt: defaultAIPolishPrompt,
	}
	if err := app.repo.SaveAdminSettings(nil, MailSettings{}, S3Settings{}, aiSettings); err != nil {
		t.Fatal(err)
	}
	app.completeAI = func(_ context.Context, _ *http.Client, _ AISettings, _, _ string, maxTokens int) (string, error) {
		switch maxTokens {
		case 500:
			return "简短摘要", nil
		case 100:
			return "Hello, World!", nil
		default:
			return "# 润色后的正文", nil
		}
	}
	cookie, _ := loginTestUser(t, app, "admin", resetAdminPassword(t, app), "")
	claimsRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	claimsRequest.AddCookie(cookie)
	claims, err := app.auth.Claims(claimsRequest)
	if err != nil {
		t.Fatal(err)
	}
	csrf := app.auth.CSRF(claims)
	tests := []struct {
		action  string
		content string
		want    string
	}{
		{action: "summary", content: "文章正文", want: "简短摘要"},
		{action: "slug", want: "hello-world"},
		{action: "polish", content: "原始正文", want: "# 润色后的正文"},
	}
	for _, test := range tests {
		form := url.Values{
			"csrf": {csrf}, "action": {test.action}, "title": {"测试标题"}, "content": {test.content},
		}
		request := httptest.NewRequest(http.MethodPost, "/admin/ai", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), test.want) {
			t.Fatalf("AI %s status=%d body=%s", test.action, response.Code, response.Body.String())
		}
	}

	settingsRequest := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	settingsRequest.AddCookie(cookie)
	settingsResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(settingsResponse, settingsRequest)
	body := settingsResponse.Body.String()
	if strings.Contains(body, "hidden-ai-key") || !strings.Contains(body, "已配置，留空保持不变") {
		t.Fatal("AI API key was exposed or its configured placeholder is missing")
	}
	editorRequest := httptest.NewRequest(http.MethodGet, "/admin/write", nil)
	editorRequest.AddCookie(cookie)
	editorResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(editorResponse, editorRequest)
	for _, label := range []string{"翻译 Slug", "生成摘要", "润色正文"} {
		if !strings.Contains(editorResponse.Body.String(), label) {
			t.Fatalf("editor does not contain %q", label)
		}
	}
}

func TestAIRequestBodyLimitRunsBeforeCSRFParsing(t *testing.T) {
	app := newTestApp(t)
	cookie, _ := loginTestUser(t, app, "admin", resetAdminPassword(t, app), "")
	claimsRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	claimsRequest.AddCookie(cookie)
	claims, err := app.auth.Claims(claimsRequest)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"csrf":    {app.auth.CSRF(claims)},
		"action":  {"summary"},
		"content": {strings.Repeat("a", (2<<20)+1)},
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/ai", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized AI request status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMissingLocalUploadRedirectsToS3(t *testing.T) {
	app := newTestApp(t)
	if err := app.repo.SaveAdminSettings(nil, MailSettings{}, S3Settings{
		Enabled: true, Endpoint: "https://s3.example.com", Region: "auto", Bucket: "media",
		AccessKey: "access-key", SecretKey: "secret-key", PublicURL: "https://cdn.example.com",
		PathPrefix: "uploads",
	}, AISettings{}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/uploads/2025/03/cover.png", nil)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusTemporaryRedirect {
		t.Fatalf("missing upload status = %d, body = %s", response.Code, response.Body.String())
	}
	if location := response.Header().Get("Location"); location != "https://cdn.example.com/uploads/2025/03/cover.png" {
		t.Fatalf("missing upload redirect = %q", location)
	}
}

func TestTagSlugsAreUniqueAndSummariesExcludeContent(t *testing.T) {
	app := newTestApp(t)
	creator, err := app.repo.UserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	for index, tag := range []string{"Go!", "Go?"} {
		_, err := app.repo.SavePost(Post{
			AuthorID: creator.ID, Slug: fmt.Sprintf("tag-%d", index), Title: tag, Content: "large article body",
			Kind: "post", Status: "published", Tags: []Tag{{Label: tag}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	tags, err := app.repo.Tags()
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 2 || tags[0].Slug == tags[1].Slug {
		t.Fatalf("tag slugs are not unique: %#v", tags)
	}
	for _, tag := range tags {
		posts, label, err := app.repo.PostsByTag(tag.Slug)
		if err != nil {
			t.Fatal(err)
		}
		if label != tag.Label || len(posts) != 1 {
			t.Fatalf("tag route %q returned label %q and %d posts", tag.Slug, label, len(posts))
		}
		if posts[0].Content != "" {
			t.Fatal("summary query loaded article content")
		}
	}
}

func TestRequestLogUsesHTTPErrorStatus(t *testing.T) {
	app := newTestApp(t)
	var output bytes.Buffer
	app.logger = slog.New(slog.NewTextHandler(&output, nil))
	e := echo.New()
	c := e.NewContext(httptest.NewRequest(http.MethodGet, "/missing", nil), httptest.NewRecorder())
	err := app.logMiddleware(func(echo.Context) error {
		return echo.NewHTTPError(http.StatusTeapot)
	})(c)
	if err == nil {
		t.Fatal("middleware swallowed HTTP error")
	}
	if !strings.Contains(output.String(), "status=418") {
		t.Fatalf("log does not contain HTTP error status: %s", output.String())
	}
}

func addTestUser(t *testing.T, app *App, username, password string) User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	result, err := app.database.Exec(
		`INSERT INTO users(username,password_hash,nickname,email,avatar_url,website_url,signature,created_at,
qq_url,wechat_url,weibo_url,x_url,telegram_url,mastodon_url,bilibili_url,instagram_url,tiktok_url)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		username, string(hash), username, username+"@example.com", "", "", "", time.Now().Unix(),
		"", "", "", "", "", "", "", "", "",
	)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	user, err := app.repo.UserByID(id)
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func resetAdminPassword(t *testing.T, app *App) string {
	t.Helper()
	password := "test-password"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.database.Exec("UPDATE users SET password_hash=? WHERE username='admin'", string(hash)); err != nil {
		t.Fatal(err)
	}
	return password
}

func loginTestUser(t *testing.T, app *App, username, password, next string) (*http.Cookie, *httptest.ResponseRecorder) {
	t.Helper()
	path := "/login"
	if next != "" {
		path += "?next=" + url.QueryEscape(next)
	}
	form := url.Values{"username": {username}, "password": {password}}
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.RemoteAddr = "192.0.2.10:1234"
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, body = %s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login did not set a session cookie")
	}
	return cookies[0], response
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

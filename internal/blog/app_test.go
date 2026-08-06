package blog

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

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

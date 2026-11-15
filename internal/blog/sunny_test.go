package blog

import (
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func saveSunnyTestPost(t *testing.T, app *App, post Post) int64 {
	t.Helper()
	if post.Kind == "" {
		post.Kind = "post"
	}
	if post.Status == "" {
		post.Status = "published"
	}
	if post.Title == "" {
		post.Title = post.Slug
	}
	id, err := app.repo.SavePost(post)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSunnyPublicProfileKeepsCreatorIdentity(t *testing.T) {
	app := newTestApp(t)
	creator, err := app.repo.UserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	addTestUser(t, app, "other", "reader-password")
	if _, err := app.database.Exec("UPDATE users SET nickname=?,avatar_url=?,signature=? WHERE id=?", "站长", "https://example.com/avatar.png", "日常记录", creator.ID); err != nil {
		t.Fatal(err)
	}
	owner, err := app.repo.SiteOwner()
	if err != nil || owner != (PublicProfile{ID: creator.ID, Name: "站长", AvatarURL: "https://example.com/avatar.png", Signature: "日常记录"}) {
		t.Fatalf("public creator profile=%#v error=%v", owner, err)
	}
	if _, err := app.database.Exec("DELETE FROM users WHERE id=?", creator.ID); err != nil {
		t.Fatal(err)
	}
	owner, err = app.repo.SiteOwner()
	if err != nil || owner != (PublicProfile{}) {
		t.Fatalf("deleted creator was replaced: profile=%#v error=%v", owner, err)
	}
}

func TestSunnySidebarOnlyShowsPublishedContentAndApprovedComments(t *testing.T) {
	app := newTestApp(t)
	for _, category := range []Category{{Name: "公开分类", Slug: "public"}, {Name: "私有分类", Slug: "private"}} {
		if err := app.repo.SaveCategory(category); err != nil {
			t.Fatal(err)
		}
	}
	categories, err := app.repo.Categories()
	if err != nil {
		t.Fatal(err)
	}
	categoryIDs := map[string]int64{}
	for _, category := range categories {
		categoryIDs[category.Slug] = category.ID
	}
	publicID := saveSunnyTestPost(t, app, Post{Slug: "public-post", CategoryID: categoryIDs["public"], Tags: ParseTags("公开标签")})
	draftID := saveSunnyTestPost(t, app, Post{Slug: "draft-post", Status: "draft", CategoryID: categoryIDs["private"], Tags: ParseTags("草稿标签")})
	futureID := saveSunnyTestPost(t, app, Post{Slug: "future-post", PublishedAt: time.Now().Add(time.Hour).Unix(), Tags: ParseTags("未来标签")})
	pageID := saveSunnyTestPost(t, app, Post{Slug: "about", Kind: "page"})
	var newestID int64
	for i := 0; i < 7; i++ {
		postID := publicID
		if i == 6 {
			postID = pageID
		}
		newestID, err = app.repo.CreateCommentReturningID(Comment{PostID: postID, AuthorName: "reader", AuthorEmail: "private@example.com", Content: fmt.Sprintf("公开评论%d", i)}, "hash", "127.0.0.1", "test", false)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, comment := range []struct {
		postID  int64
		pending bool
	}{{publicID, true}, {draftID, false}, {futureID, false}} {
		if err := app.repo.CreateComment(Comment{PostID: comment.postID, AuthorName: "secret", AuthorEmail: "private@example.com", Content: "不公开评论"}, "hash", "127.0.0.1", "test", comment.pending); err != nil {
			t.Fatal(err)
		}
	}
	data := PageData{Tags: []Tag{{Label: "页面自己的标签"}}, Categories: []Category{{Name: "页面自己的分类"}}}
	if err := app.loadPublicData(&data); err != nil {
		t.Fatal(err)
	}
	if len(data.SidebarTags) != 1 || data.SidebarTags[0].Label != "公开标签" {
		t.Fatalf("sidebar leaked private tags: %#v", data.SidebarTags)
	}
	if len(data.SidebarCategories) != 1 || data.SidebarCategories[0].Slug != "public" || data.SidebarCategories[0].Count != 1 {
		t.Fatalf("sidebar category counts include private posts: %#v", data.SidebarCategories)
	}
	if data.Tags[0].Label != "页面自己的标签" || data.Categories[0].Name != "页面自己的分类" {
		t.Fatal("sidebar loading overwrote the page's own data")
	}
	if len(data.RecentComments) != 5 || data.RecentComments[0].ID != newestID || data.RecentComments[0].PostKind != "page" || data.RecentComments[0].PostSlug != "about" {
		t.Fatalf("recent comments have wrong order, limit or page link: %#v", data.RecentComments)
	}
	for _, comment := range data.RecentComments {
		if comment.AuthorName == "secret" || comment.Content == "不公开评论" {
			t.Fatalf("sidebar leaked a private comment: %#v", comment)
		}
	}
}

func TestSunnyPublicRenderLoadsSharedDataBeforeWritingHeaders(t *testing.T) {
	app := newTestApp(t)
	app.templates = template.Must(template.New("root").Parse(`{{define "public"}}{{.SiteOwner.Name}}{{end}}{{define "auth"}}{{.SiteOwner.Name}}{{end}}`))
	for _, name := range []string{"public", "auth"} {
		response := httptest.NewRecorder()
		context := app.echo.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), response)
		if err := app.render(context, http.StatusOK, name, PageData{}); err != nil || response.Body.String() != "admin" {
			t.Fatalf("%s shared data missing: body=%q error=%v", name, response.Body.String(), err)
		}
	}
	if _, err := app.database.Exec("DROP TABLE site_identity"); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	context := app.echo.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), response)
	if err := app.render(context, http.StatusOK, "public", PageData{}); err == nil || context.Response().Committed || response.Body.Len() != 0 {
		t.Fatalf("public data failure committed a partial response: error=%v committed=%v body=%q", err, context.Response().Committed, response.Body.String())
	}
	response = httptest.NewRecorder()
	app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusInternalServerError || response.Body.Len() == 0 {
		t.Fatalf("public data failure did not return a useful error: status=%d body=%q", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "site_identity") || strings.Contains(response.Body.String(), "SQL") {
		t.Fatalf("public data failure leaked database diagnostics: %q", response.Body.String())
	}
}

func TestSunnySearchPreservesExistingPageAndCreatorPreview(t *testing.T) {
	app := newTestApp(t)
	creator, err := app.repo.UserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	pageID := saveSunnyTestPost(t, app, Post{Slug: "search", Kind: "page", Title: "Existing page", Content: "Original content"})
	app.templates = template.Must(template.New("root").Parse(`{{define "public"}}view={{.View}};title={{.Post.Title}};query={{.SearchQuery}};{{end}}`))
	for _, test := range []struct {
		path string
		view string
	}{{"/search", "page"}, {"/search?comment=submitted", "page"}, {"/search?q=", "search"}, {"/search?q=hello", "search"}, {"/search?page=1", "search"}} {
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "view="+test.view+";") {
			t.Fatalf("existing /search page or explicit query lost: path=%q status=%d body=%q", test.path, response.Code, response.Body.String())
		}
	}
	for _, change := range []string{"status='draft'", fmt.Sprintf("status='published',published_at=%d", time.Now().Add(time.Hour).Unix())} {
		if _, err := app.database.Exec("UPDATE posts SET "+change+" WHERE id=?", pageID); err != nil {
			t.Fatal(err)
		}
		for _, user := range []*User{nil, &creator} {
			response := httptest.NewRecorder()
			context := app.echo.NewContext(httptest.NewRequest(http.MethodGet, "/search", nil), response)
			if user != nil {
				context.Set("user", user)
			}
			if err := app.search(context); err != nil {
				t.Fatal(err)
			}
			view := "search"
			if user != nil {
				view = "page"
			}
			if !strings.Contains(response.Body.String(), "view="+view+";") {
				t.Fatalf("private /search page visibility changed: user=%v body=%q", user, response.Body.String())
			}
		}
	}
}

func TestSunnyPublicProfileEscapesAvatarAndSignature(t *testing.T) {
	app := newTestApp(t)
	if _, err := app.database.Exec("UPDATE users SET avatar_url=?,signature=? WHERE username='admin'", "javascript:alert(1337)", "<script>alert(1337)</script>"); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `src="#ZgotmplZ"`) || !strings.Contains(body, "&lt;script&gt;alert(1337)&lt;/script&gt;") {
		t.Fatalf("public profile did not escape imported values: status=%d body=%q", response.Code, body)
	}
	if strings.Contains(body, "javascript:alert") || strings.Contains(body, "<script>alert(1337)") || strings.Contains(body, "admin@example.com") {
		t.Fatal("public profile leaked private details or executable markup")
	}
}

func TestSunnySearchFiltersWildcardsInjectionAndPrivateContent(t *testing.T) {
	app := newTestApp(t)
	for _, post := range []Post{
		{Slug: "title", Title: "Needle title"},
		{Slug: "excerpt", Title: "Article two", Excerpt: "needle excerpt"},
		{Slug: "body", Title: "Article three", Content: "needle body"},
		{Slug: "draft", Title: "needle private", Status: "draft"},
		{Slug: "future", Title: "needle future", PublishedAt: time.Now().Add(time.Hour).Unix()},
		{Slug: "page", Title: "needle page", Kind: "page"},
		{Slug: "percent", Title: "100% accurate"},
		{Slug: "underscore", Title: "snake_case"},
		{Slug: "escape", Title: "Hello!"},
	} {
		saveSunnyTestPost(t, app, post)
	}
	for _, test := range []struct {
		query string
		total int
		slug  string
	}{{"NEEDLE", 3, ""}, {"%", 1, "percent"}, {"_", 1, "underscore"}, {"!", 1, "escape"}, {"' OR 1=1 --", 0, ""}, {"missing", 0, ""}} {
		posts, total, err := app.repo.SearchPublished(test.query, 20, 0)
		if err != nil || total != test.total || len(posts) != total {
			t.Fatalf("query %q: total=%d posts=%#v error=%v", test.query, total, posts, err)
		}
		if test.slug != "" && posts[0].Slug != test.slug {
			t.Fatalf("LIKE query %q matched the wrong post: %#v", test.query, posts)
		}
	}
	if _, err := app.database.Exec("UPDATE settings SET value='2' WHERE name='posts_per_page'"); err != nil {
		t.Fatal(err)
	}
	app.templates = template.Must(template.New("root").Parse(`{{define "public"}}view={{.View}};query={{.SearchQuery}};page={{.Page}};pages={{.TotalPages}};total={{.Total}};{{range .Posts}}post={{.Slug}};{{end}}{{end}}`))
	for _, test := range []struct {
		query  string
		page   string
		status int
		body   string
	}{{"needle", "1", 200, "page=1;pages=2;total=3;post=body;post=excerpt;"}, {"needle", "2", 200, "page=2;pages=2;total=3;post=title;"}, {"needle", "3", 404, ""}, {"", "", 200, "query=;page=1;pages=1;total=0;"}, {"", "2", 404, ""}, {"missing", "", 200, "total=0;"}, {"needle", "0", 400, ""}, {"needle", "-1", 400, ""}, {"needle", "abc", 400, ""}, {"needle", "9999999999999999999999", 400, ""}, {strings.Repeat("字", 201), "", 400, ""}} {
		query := url.Values{"q": {test.query}, "page": {test.page}}
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/search?"+query.Encode(), nil))
		if response.Code != test.status || test.body != "" && !strings.Contains(response.Body.String(), test.body) {
			t.Fatalf("query=%q page=%q: status=%d body=%q", test.query, test.page, response.Code, response.Body.String())
		}
	}
}

package blog

import (
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jkjoy/go-blog/web"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

type App struct {
	cfg       Config
	logger    *slog.Logger
	version   string
	database  *Database
	repo      *Repository
	auth      *Auth
	markdown  *Markdown
	templates *template.Template
	echo      *echo.Echo
}

type PageData struct {
	View          string
	Title         string
	Description   string
	Canonical     string
	Settings      SiteSettings
	User          *User
	NavPages      []Post
	Posts         []Post
	Post          Post
	Comments      []Comment
	CommentCount  int
	CanComment    bool
	AdminComments []AdminComment
	Categories    []Category
	Tags          []Tag
	Links         []Link
	Stats         DashboardStats
	Page          int
	TotalPages    int
	Total         int
	Years         []ArchiveYear
	Tag           Tag
	Category      Category
	Error         string
	Notice        string
	CSRF          string
	Version       string
}

type ArchiveYear struct {
	Year  int
	Posts []Post
}

func New(cfg Config, logger *slog.Logger, version string) (*App, error) {
	if err := os.MkdirAll(cfg.UploadDir, 0o755); err != nil {
		return nil, fmt.Errorf("create upload directory: %w", err)
	}
	database, err := OpenDatabase(cfg, logger)
	if err != nil {
		return nil, err
	}
	if err := database.EnsureInitialData(logger); err != nil {
		database.DB.Close()
		return nil, fmt.Errorf("initialize defaults: %w", err)
	}
	auth, ephemeral, err := NewAuth(cfg.SessionSecret, cfg.SecureCookies)
	if err != nil {
		database.DB.Close()
		return nil, err
	}
	if ephemeral {
		logger.Warn("BLOG_SESSION_SECRET is not set; login sessions will expire when the process restarts")
	}

	templates, err := parseTemplates()
	if err != nil {
		database.DB.Close()
		return nil, err
	}
	app := &App{
		cfg: cfg, logger: logger, version: version, database: database, repo: NewRepository(database),
		auth: auth, markdown: NewMarkdown(), templates: templates,
	}
	app.echo = app.routes()
	return app, nil
}

func (a *App) Close() error          { return a.database.DB.Close() }
func (a *App) Handler() http.Handler { return a.echo }

func parseTemplates() (*template.Template, error) {
	functions := template.FuncMap{
		"date":     func(timestamp int64, format string) string { return time.Unix(timestamp, 0).Format(format) },
		"safeHTML": func(value string) template.HTML { return template.HTML(value) },
		"nl2br": func(value string) template.HTML {
			return template.HTML(strings.ReplaceAll(template.HTMLEscapeString(value), "\n", "<br>"))
		},
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"seq": func(start, end int) []int {
			if end < start {
				return nil
			}
			items := make([]int, end-start+1)
			for i := range items {
				items[i] = start + i
			}
			return items
		},
		"joinTags": func(tags []Tag) string {
			labels := make([]string, len(tags))
			for i, tag := range tags {
				labels[i] = tag.Label
			}
			return strings.Join(labels, ", ")
		},
		"tagSize": func(count, max int) int {
			if max < 1 {
				return 1
			}
			size := 1 + count*4/max
			if size > 5 {
				size = 5
			}
			return size
		},
		"firstRune": func(value string) string {
			for _, r := range value {
				return string(r)
			}
			return "?"
		},
		"footer":     func(value string) string { return strings.ReplaceAll(value, "{year}", strconvItoa(time.Now().Year())) },
		"mod3":       func(value int) int { return value % 3 },
		"coverIndex": func(value int) int { return value%6 + 1 },
		"pathEscape": url.PathEscape,
	}
	return template.New("root").Funcs(functions).ParseFS(web.Files, "templates/*.html")
}

func strconvItoa(value int) string { return fmt.Sprintf("%d", value) }

func (a *App) routes() *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.HTTPErrorHandler = a.httpError
	e.Pre(middleware.RemoveTrailingSlash())
	e.Use(middleware.Recover())
	e.Use(middleware.RequestID())
	e.Use(middleware.SecureWithConfig(middleware.SecureConfig{
		XSSProtection: "0", ContentTypeNosniff: "nosniff", XFrameOptions: "SAMEORIGIN",
		HSTSMaxAge: 31536000, ContentSecurityPolicy: "default-src 'self'; img-src 'self' data: https:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; frame-src https://www.youtube.com https://player.bilibili.com; connect-src 'self'",
	}))
	e.Use(a.sessionMiddleware)
	e.Use(a.logMiddleware)

	staticFS, _ := fs.Sub(web.Files, "static")
	e.GET("/static/*", echo.WrapHandler(http.StripPrefix("/static/", http.FileServer(http.FS(staticFS)))))
	uploadServer := http.StripPrefix("/uploads/", http.FileServer(http.Dir(a.cfg.UploadDir)))
	e.GET("/uploads/*", func(c echo.Context) error {
		if strings.HasSuffix(c.Request().URL.Path, "/") {
			return echo.NewHTTPError(http.StatusNotFound)
		}
		uploadServer.ServeHTTP(c.Response(), c.Request())
		return nil
	})
	e.GET("/healthz", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok", "version": a.version})
	})

	e.GET("/", a.home)
	e.GET("/page/:page", a.home)
	e.GET("/archive/:slug", a.post)
	e.GET("/archives", a.archives)
	e.GET("/tags", a.tags)
	e.GET("/tag/:slug", a.tag)
	e.GET("/category/:slug", a.category)
	e.GET("/links", a.links)
	e.GET("/rss.xml", a.rss)
	e.GET("/sitemap.xml", a.sitemap)
	e.POST("/comments", a.submitComment)
	e.GET("/login", a.loginPage)
	e.POST("/login", a.login)
	e.POST("/logout", a.logout)

	admin := e.Group("/admin", a.requireAdmin)
	admin.GET("", a.adminDashboard)
	admin.GET("/posts", a.adminPosts)
	admin.GET("/comments", a.adminComments)
	admin.POST("/comments/:id", a.moderateComment)
	admin.GET("/categories", a.adminCategories)
	admin.POST("/categories", a.saveCategory)
	admin.POST("/categories/:id/delete", a.deleteCategory)
	admin.GET("/links", a.adminLinks)
	admin.POST("/links", a.saveLink)
	admin.POST("/links/:id/delete", a.deleteLink)
	admin.GET("/settings", a.adminSettings)
	admin.POST("/settings", a.saveSettings)
	admin.POST("/upload", a.uploadAttachment)
	admin.GET("/write", a.editor)
	admin.POST("/write", a.saveEditor)
	admin.GET("/edit/:id", a.editor)
	admin.POST("/edit/:id", a.saveEditor)
	admin.POST("/delete/:id", a.deletePost)

	e.GET("/:slug", a.page)
	return e
}

func (a *App) logMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		start := time.Now()
		err := next(c)
		a.logger.Info("http request", "method", c.Request().Method, "path", c.Request().URL.Path,
			"status", c.Response().Status, "duration", time.Since(start), "request_id", c.Response().Header().Get(echo.HeaderXRequestID))
		return err
	}
}

func (a *App) sessionMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		claims, err := a.auth.Claims(c.Request())
		if err == nil {
			if user, err := a.repo.UserByID(claims.UserID); err == nil {
				c.Set("user", &user)
				c.Set("claims", claims)
			}
		}
		return next(c)
	}
}

func (a *App) requireAdmin(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if currentUser(c) == nil {
			return c.Redirect(http.StatusSeeOther, "/login?next="+c.Request().URL.Path)
		}
		if c.Request().Method != http.MethodGet && c.Request().Method != http.MethodHead {
			claims, ok := c.Get("claims").(sessionClaims)
			if !ok || !a.auth.ValidCSRF(claims, c.FormValue("csrf")) {
				return echo.NewHTTPError(http.StatusForbidden, "请求校验失败，请刷新页面后重试")
			}
		}
		return next(c)
	}
}

func currentUser(c echo.Context) *User {
	user, _ := c.Get("user").(*User)
	return user
}

func (a *App) httpError(err error, c echo.Context) {
	code := http.StatusInternalServerError
	message := "服务器暂时无法处理这个请求"
	if httpErr, ok := err.(*echo.HTTPError); ok {
		code = httpErr.Code
		if text, ok := httpErr.Message.(string); ok {
			message = text
		}
	}
	if code >= 500 {
		a.logger.Error("request failed", "error", err, "path", c.Request().URL.Path)
	}
	data, dataErr := a.baseData(c, "出错了")
	if dataErr == nil {
		data.View, data.Error = "error", message
		_ = a.render(c, code, "public", data)
		return
	}
	_ = c.String(code, message)
}

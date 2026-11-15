package blog

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jkjoy/go-blog/web"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

type App struct {
	cfg                Config
	logger             *slog.Logger
	version            string
	database           *Database
	repo               *Repository
	auth               *Auth
	markdown           *Markdown
	templates          *template.Template
	echo               *echo.Echo
	loginIPRate        *rateLimiter
	loginAccountRate   *rateLimiter
	commentBurstRate   *rateLimiter
	commentHourRate    *rateLimiter
	aiRate             *rateLimiter
	registerIPRate     *rateLimiter
	sendMail           mailSender
	putObject          objectUploader
	completeAI         aiCompleter
	aiClient           *http.Client
	notificationMu     sync.Mutex
	notificationCancel context.CancelFunc
	notificationDone   chan struct{}
	notificationWake   chan struct{}
	notificationClosed bool
}

type PageData struct {
	View              string
	Title             string
	Description       string
	Canonical         string
	Settings          SiteSettings
	SiteOwner         PublicProfile
	SidebarTags       []Tag
	SidebarCategories []Category
	RecentComments    []PublicComment
	SearchQuery       string
	User              *User
	NavPages          []Post
	Posts             []Post
	Post              Post
	Comments          []Comment
	CommentCount      int
	CanComment        bool
	AdminComments     []AdminComment
	NotificationJobs  []NotificationJob
	Categories        []Category
	Tags              []Tag
	Links             []Link
	Stats             DashboardStats
	Mail              MailSettings
	S3                S3Settings
	AI                AISettings
	AIReady           bool
	FirstUser         bool
	Registration      Registration
	Page              int
	TotalPages        int
	Total             int
	Years             []ArchiveYear
	Tag               Tag
	Category          Category
	Link              Link
	Error             string
	Notice            string
	CSRF              string
	Version           string
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
	repo := NewRepository(database)
	if err := repo.EnsureTagMetadata(); err != nil {
		database.DB.Close()
		return nil, fmt.Errorf("initialize tag metadata: %w", err)
	}
	app := &App{
		notificationWake: make(chan struct{}, 1),
		cfg:              cfg, logger: logger, version: version, database: database, repo: repo,
		auth: auth, markdown: NewMarkdown(), templates: templates,
		loginIPRate: newRateLimiter(8, 5*time.Minute), loginAccountRate: newRateLimiter(20, 15*time.Minute),
		commentBurstRate: newRateLimiter(5, time.Minute), commentHourRate: newRateLimiter(30, time.Hour),
		aiRate: newRateLimiter(20, time.Minute), registerIPRate: newRateLimiter(5, time.Hour),
		sendMail: sendSMTPMail, putObject: uploadS3Object,
		completeAI: requestAICompletion, aiClient: &http.Client{Timeout: 75 * time.Second},
	}
	app.echo, err = app.routes()
	if err != nil {
		database.DB.Close()
		return nil, err
	}
	return app, nil
}

func (a *App) Close() error {
	a.notificationMu.Lock()
	a.notificationClosed = true
	cancel, done := a.notificationCancel, a.notificationDone
	a.notificationMu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	return a.database.DB.Close()
}
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

func (a *App) routes() (*echo.Echo, error) {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.HTTPErrorHandler = a.httpError
	if err := configureIPExtractor(e, a.cfg.TrustedProxy); err != nil {
		return nil, err
	}
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
		relative := strings.TrimPrefix(path.Clean("/"+c.Param("*")), "/")
		if relative == "" || strings.HasSuffix(c.Request().URL.Path, "/") {
			return echo.NewHTTPError(http.StatusNotFound)
		}
		localPath := filepath.Join(a.cfg.UploadDir, filepath.FromSlash(relative))
		if info, err := os.Stat(localPath); err == nil && !info.IsDir() {
			uploadServer.ServeHTTP(c.Response(), c.Request())
			return nil
		}
		s3Settings, err := a.repo.S3Settings()
		if err == nil && s3Settings.Enabled && s3Settings.PublicURL != "" {
			return c.Redirect(http.StatusTemporaryRedirect, s3Settings.objectURL(path.Join(s3Settings.PathPrefix, relative)))
		}
		return echo.NewHTTPError(http.StatusNotFound)
	})
	e.GET("/healthz", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok", "version": a.version})
	})

	e.GET("/", a.home)
	e.GET("/search", a.search)
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
	e.GET("/register", a.registerPage)
	e.POST("/register", a.register)
	e.POST("/logout", a.logout)
	e.GET("/account", a.accountPage, a.requireUser)
	e.POST("/account", a.saveAccount, a.requireUser)

	admin := e.Group("/admin", a.requireAdmin)
	admin.GET("", a.adminDashboard)
	admin.GET("/posts", a.adminPosts)
	admin.GET("/comments", a.adminComments)
	admin.POST("/comments/:id", a.moderateComment)
	admin.GET("/notifications", a.adminNotifications)
	admin.POST("/notifications/:id/:kind/retry", a.retryNotification)
	admin.GET("/categories", a.adminCategories)
	admin.POST("/categories", a.saveCategory)
	admin.POST("/categories/:id/delete", a.deleteCategory)
	admin.GET("/links", a.adminLinks)
	admin.POST("/links", a.saveLink)
	admin.POST("/links/:id/delete", a.deleteLink)
	admin.GET("/settings", a.adminSettings)
	admin.POST("/settings", a.saveSettings)
	admin.POST("/upload", a.uploadAttachment)
	admin.POST("/ai", a.aiAction)
	admin.GET("/write", a.editor)
	admin.POST("/write", a.saveEditor)
	admin.GET("/edit/:id", a.editor)
	admin.POST("/edit/:id", a.saveEditor)
	admin.POST("/delete/:id", a.deletePost)

	e.GET("/:slug", a.page)
	return e, nil
}

func configureIPExtractor(e *echo.Echo, trustedProxy string) error {
	trustedProxy = strings.TrimSpace(trustedProxy)
	if trustedProxy == "" {
		e.IPExtractor = echo.ExtractIPDirect()
		return nil
	}

	options := []echo.TrustOption{
		echo.TrustLoopback(false),
		echo.TrustLinkLocal(false),
		echo.TrustPrivateNet(false),
	}
	for _, value := range strings.Split(trustedProxy, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			ip := net.ParseIP(value)
			if ip == nil {
				return fmt.Errorf("invalid BLOG_TRUSTED_PROXY address %q", value)
			}
			bits := 128
			if ipv4 := ip.To4(); ipv4 != nil {
				ip, bits = ipv4, 32
			}
			network = &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}
		}
		options = append(options, echo.TrustIPRange(network))
	}
	if len(options) == 3 {
		return errors.New("BLOG_TRUSTED_PROXY must contain at least one IP address or CIDR")
	}
	e.IPExtractor = echo.ExtractIPFromXFFHeader(options...)
	return nil
}

func (a *App) logMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		start := time.Now()
		err := next(c)
		status := c.Response().Status
		if status == 0 {
			status = http.StatusOK
		}
		if err != nil {
			status = http.StatusInternalServerError
			var httpErr *echo.HTTPError
			if errors.As(err, &httpErr) {
				status = httpErr.Code
			}
		}
		a.logger.Info("http request", "method", c.Request().Method, "path", c.Request().URL.Path,
			"status", status, "duration", time.Since(start), "request_id", c.Response().Header().Get(echo.HeaderXRequestID))
		return err
	}
}

func (a *App) sessionMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		claims, err := a.auth.Claims(c.Request())
		if err == nil {
			if user, err := a.repo.UserByID(claims.UserID); err == nil && a.auth.ValidSessionUser(claims, user) {
				c.Set("user", &user)
				c.Set("claims", claims)
			}
		}
		return next(c)
	}
}

func (a *App) requireAdmin(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		user := currentUser(c)
		if user == nil {
			if isAdminAPIPath(c.Request().URL.Path) {
				return echo.NewHTTPError(http.StatusUnauthorized, "登录已过期，请重新登录")
			}
			return c.Redirect(http.StatusSeeOther, "/login?next="+c.Request().URL.Path)
		}
		if !user.IsCreator {
			return echo.NewHTTPError(http.StatusForbidden, "只有站点创建者可以访问管理后台")
		}
		if c.Request().Method != http.MethodGet && c.Request().Method != http.MethodHead {
			limit := int64(2 << 20)
			if c.Request().URL.Path == "/admin/upload" {
				limit = 12 << 20
			}
			request := c.Request()
			request.Body = http.MaxBytesReader(c.Response(), request.Body, limit)
			mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
			if err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, "管理请求表单格式无效")
			}
			if mediaType == "multipart/form-data" {
				err = request.ParseMultipartForm(limit)
			} else {
				err = request.ParseForm()
			}
			if err != nil {
				var maxBytesError *http.MaxBytesError
				if errors.As(err, &maxBytesError) {
					return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "管理请求内容过大")
				}
				return echo.NewHTTPError(http.StatusBadRequest, "管理请求表单格式无效")
			}
			claims, ok := c.Get("claims").(sessionClaims)
			if !ok || !a.auth.ValidCSRF(claims, c.FormValue("csrf")) {
				return echo.NewHTTPError(http.StatusForbidden, "请求校验失败，请刷新页面后重试")
			}
		}
		return next(c)
	}
}

func (a *App) requireUser(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if currentUser(c) == nil {
			return c.Redirect(http.StatusSeeOther, "/login?next="+url.QueryEscape(c.Request().URL.Path))
		}
		if c.Request().Method != http.MethodGet && c.Request().Method != http.MethodHead {
			request := c.Request()
			request.Body = http.MaxBytesReader(c.Response(), request.Body, 64<<10)
			if err := request.ParseForm(); err != nil {
				return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "账户表单不能超过 64KB")
			}
			claims, ok := c.Get("claims").(sessionClaims)
			if !ok || !a.auth.ValidCSRF(claims, c.FormValue("csrf")) {
				return echo.NewHTTPError(http.StatusForbidden, "请求校验失败，请刷新页面后重试")
			}
		}
		return next(c)
	}
}

func isAdminAPIPath(path string) bool {
	return path == "/admin/ai" || path == "/admin/upload"
}

func currentUser(c echo.Context) *User {
	user, _ := c.Get("user").(*User)
	return user
}

func currentUserIsCreator(c echo.Context) bool {
	user := currentUser(c)
	return user != nil && user.IsCreator
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
	if isAdminAPIPath(c.Request().URL.Path) {
		_ = c.JSON(code, map[string]string{"message": message})
		return
	}
	data, dataErr := a.baseData(c, "出错了")
	if dataErr == nil {
		data.View, data.Error = "error", message
		if renderErr := a.render(c, code, "public", data); renderErr == nil || c.Response().Committed {
			return
		}
	}
	_ = c.String(code, message)
}

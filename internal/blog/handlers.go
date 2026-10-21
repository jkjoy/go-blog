package blog

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/text/unicode/norm"
)

func (a *App) baseData(c echo.Context, title string) (PageData, error) {
	settings, err := a.repo.Settings()
	if err != nil {
		return PageData{}, err
	}
	pages, err := a.repo.NavPages()
	if err != nil {
		return PageData{}, err
	}
	data := PageData{
		Title: title, Description: settings.Description, Settings: settings, NavPages: pages,
		User: currentUser(c), Version: a.version,
	}
	if c.QueryParam("comment") == "submitted" {
		data.Notice = "submitted"
	}
	if c.QueryParam("saved") == "1" {
		data.Notice = "saved"
	}
	if claims, ok := c.Get("claims").(sessionClaims); ok {
		data.CSRF = a.auth.CSRF(claims)
	}
	return data, nil
}

func (a *App) render(c echo.Context, status int, name string, data PageData) error {
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	c.Response().WriteHeader(status)
	return a.templates.ExecuteTemplate(c.Response(), name, data)
}

func (a *App) home(c echo.Context) error {
	page := 1
	if raw := c.Param("page"); raw != "" {
		page, _ = strconv.Atoi(raw)
	}
	if page < 1 {
		return echo.NewHTTPError(http.StatusNotFound, "页面不存在")
	}
	data, err := a.baseData(c, "")
	if err != nil {
		return err
	}
	total, err := a.repo.CountPublished()
	if err != nil {
		return err
	}
	totalPages := int(math.Ceil(float64(total) / float64(data.Settings.PostsPerPage)))
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		return echo.NewHTTPError(http.StatusNotFound, "页面不存在")
	}
	posts, err := a.repo.ListPublished(data.Settings.PostsPerPage, (page-1)*data.Settings.PostsPerPage)
	if err != nil {
		return err
	}
	a.decoratePosts(posts)
	data.View, data.Title, data.Posts, data.Total, data.Page, data.TotalPages = "home", data.Settings.Name, posts, total, page, totalPages
	return a.render(c, http.StatusOK, "public", data)
}

func (a *App) post(c echo.Context) error {
	rawIdentifier := decodedPathParam(c, "slug")
	identifier := norm.NFC.String(rawIdentifier)
	creator := currentUserIsCreator(c)
	post, err := a.repo.FindContent("post", identifier, creator)
	if IsNotFound(err) {
		return echo.NewHTTPError(http.StatusNotFound, "文章不存在")
	}
	if err != nil {
		return err
	}
	if rawIdentifier != identifier {
		return c.Redirect(http.StatusMovedPermanently, contentURL(post))
	}
	data, err := a.baseData(c, post.Title)
	if err != nil {
		return err
	}
	post.Rendered = string(a.markdown.Render(post.Content))
	post.CoverURL = PostCover(post.Content)
	comments, err := a.repo.Comments(post.ID, !creator)
	if err != nil {
		return err
	}
	data.View, data.Post, data.Comments = "post", post, comments
	data.CommentCount = countComments(comments)
	data.CanComment = data.Settings.Comments && post.AllowComments && post.Status == "published" && post.PublishedAt <= time.Now().Unix()
	data.Description = DeriveExcerpt(post, 160)
	if post.Status == "published" {
		_ = a.repo.TrackView(post.ID, a.auth.IPHash(clientIP(c)))
	}
	return a.render(c, http.StatusOK, "public", data)
}

func (a *App) page(c echo.Context) error {
	rawSlug := decodedPathParam(c, "slug")
	slug := norm.NFC.String(rawSlug)
	reserved := map[string]bool{"static": true, "healthz": true, "admin": true, "login": true, "logout": true, "register": true, "account": true}
	if reserved[slug] {
		return echo.NewHTTPError(http.StatusNotFound, "页面不存在")
	}
	creator := currentUserIsCreator(c)
	post, err := a.repo.FindContent("page", slug, creator)
	if IsNotFound(err) {
		return echo.NewHTTPError(http.StatusNotFound, "页面不存在")
	}
	if err != nil {
		return err
	}
	if rawSlug != slug {
		return c.Redirect(http.StatusMovedPermanently, contentURL(post))
	}
	data, err := a.baseData(c, post.Title)
	if err != nil {
		return err
	}
	post.Rendered = string(a.markdown.Render(post.Content))
	data.View, data.Post = "page", post
	data.Comments, err = a.repo.Comments(post.ID, !creator)
	if err != nil {
		return err
	}
	data.CommentCount = countComments(data.Comments)
	data.CanComment = data.Settings.Comments && post.AllowComments && post.Status == "published" && post.PublishedAt <= time.Now().Unix()
	return a.render(c, http.StatusOK, "public", data)
}

func (a *App) archives(c echo.Context) error {
	data, err := a.baseData(c, "归档")
	if err != nil {
		return err
	}
	posts, err := a.repo.AllPublished()
	if err != nil {
		return err
	}
	groups := map[int][]Post{}
	for _, post := range posts {
		groups[post.PublishedTime().Year()] = append(groups[post.PublishedTime().Year()], post)
	}
	years := make([]int, 0, len(groups))
	for year := range groups {
		years = append(years, year)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(years)))
	for _, year := range years {
		data.Years = append(data.Years, ArchiveYear{Year: year, Posts: groups[year]})
	}
	data.View, data.Posts, data.Total = "archives", posts, len(posts)
	return a.render(c, http.StatusOK, "public", data)
}

func (a *App) tags(c echo.Context) error {
	data, err := a.baseData(c, "标签")
	if err != nil {
		return err
	}
	data.Tags, err = a.repo.Tags()
	if err != nil {
		return err
	}
	data.View = "tags"
	return a.render(c, http.StatusOK, "public", data)
}

func (a *App) tag(c echo.Context) error {
	slug := norm.NFC.String(decodedPathParam(c, "slug"))
	posts, label, err := a.repo.PostsByTag(slug)
	if err != nil {
		return err
	}
	if label == "" {
		return echo.NewHTTPError(http.StatusNotFound, "标签不存在")
	}
	data, err := a.baseData(c, "# "+label)
	if err != nil {
		return err
	}
	data.View, data.Posts, data.Tag = "timeline", posts, Tag{Label: label, Slug: slug, Count: len(posts)}
	return a.render(c, http.StatusOK, "public", data)
}

func (a *App) category(c echo.Context) error {
	posts, category, err := a.repo.PostsByCategory(norm.NFC.String(decodedPathParam(c, "slug")))
	if IsNotFound(err) {
		return echo.NewHTTPError(http.StatusNotFound, "分类不存在")
	}
	if err != nil {
		return err
	}
	data, err := a.baseData(c, category.Name)
	if err != nil {
		return err
	}
	data.View, data.Posts, data.Category = "timeline", posts, category
	return a.render(c, http.StatusOK, "public", data)
}

func decodedPathParam(c echo.Context, name string) string {
	value := c.Param(name)
	if c.Request().URL.RawPath == "" {
		return value
	}
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return value
	}
	return decoded
}

func (a *App) links(c echo.Context) error {
	data, err := a.baseData(c, "友链")
	if err != nil {
		return err
	}
	data.Links, err = a.repo.Links()
	if err != nil {
		return err
	}
	data.View = "links"
	return a.render(c, http.StatusOK, "public", data)
}

func (a *App) decoratePosts(posts []Post) {
	for i := range posts {
		posts[i].CoverURL = PostCover(posts[i].Content)
		posts[i].Excerpt = DeriveExcerpt(posts[i], 100)
	}
}

func (a *App) submitComment(c echo.Context) error {
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, 64<<10)
	if err := c.Request().ParseForm(); err != nil {
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "评论表单不能超过 64KB")
	}
	ip := clientIP(c)
	if allowed, retry := a.commentBurstRate.Allow(ip); !allowed {
		c.Response().Header().Set("Retry-After", strconv.Itoa(max(1, int(retry.Seconds()))))
		return echo.NewHTTPError(http.StatusTooManyRequests, "评论提交过于频繁，请稍后再试")
	}
	if allowed, retry := a.commentHourRate.Allow(ip); !allowed {
		c.Response().Header().Set("Retry-After", strconv.Itoa(max(1, int(retry.Seconds()))))
		return echo.NewHTTPError(http.StatusTooManyRequests, "评论提交过于频繁，请稍后再试")
	}
	postID := parseID(c.FormValue("post_id"))
	post, err := a.repo.FindPostByID(postID)
	if err != nil || !post.AllowComments || a.repo.Setting("comments_enabled") == "0" || post.Status != "published" || post.PublishedAt > time.Now().Unix() {
		return echo.NewHTTPError(http.StatusBadRequest, "这篇内容不接受评论")
	}
	comment := Comment{
		PostID: postID, ParentID: parseID(c.FormValue("parent_id")),
		AuthorName: strings.TrimSpace(c.FormValue("author_name")), AuthorEmail: strings.TrimSpace(c.FormValue("author_email")),
		AuthorURL: strings.TrimSpace(c.FormValue("author_url")), Content: strings.TrimSpace(c.FormValue("content")),
	}
	if user := currentUser(c); user != nil {
		comment.UserID, comment.AuthorName, comment.AuthorEmail, comment.AuthorURL = user.ID, user.DisplayName(), user.Email, user.WebsiteURL
	}
	if len([]rune(comment.AuthorName)) < 1 || len([]rune(comment.AuthorName)) > 50 {
		return echo.NewHTTPError(http.StatusBadRequest, "昵称长度应为 1 至 50 个字符")
	}
	if _, err := mail.ParseAddress(comment.AuthorEmail); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "邮箱格式不正确")
	}
	if len([]rune(comment.Content)) < 2 || len([]rune(comment.Content)) > 3000 {
		return echo.NewHTTPError(http.StatusBadRequest, "评论长度应为 2 至 3000 个字符")
	}
	if comment.AuthorURL != "" {
		parsed, err := url.ParseRequestURI(comment.AuthorURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return echo.NewHTTPError(http.StatusBadRequest, "网站地址格式不正确")
		}
	}
	if comment.ParentID > 0 {
		var parentPost int64
		if err := a.database.QueryRow("SELECT post_id,author_name FROM comments WHERE id=?", comment.ParentID).Scan(&parentPost, &comment.ReplyToName); err != nil || parentPost != postID {
			return echo.NewHTTPError(http.StatusBadRequest, "回复目标不存在")
		}
	}
	requireApproval := a.repo.CommentApprovalRequired()
	_, err = a.repo.CreateCommentReturningID(comment, a.auth.IPHash(ip), ip, c.Request().UserAgent(), requireApproval,
		absoluteURL(c, contentURL(post)), absoluteURL(c, "/admin/comments"))
	if err != nil {
		return err
	}
	a.wakeNotifications()
	return c.Redirect(http.StatusSeeOther, contentURL(post)+"?comment=submitted#comments")
}

func (a *App) loginPage(c echo.Context) error {
	if user := currentUser(c); user != nil {
		if user.IsCreator {
			return c.Redirect(http.StatusSeeOther, "/admin")
		}
		return c.Redirect(http.StatusSeeOther, "/")
	}
	data, err := a.baseData(c, "登录")
	if err != nil {
		return err
	}
	data.View = "login"
	return a.render(c, http.StatusOK, "auth", data)
}

func (a *App) registerPage(c echo.Context) error {
	if currentUser(c) != nil {
		return c.Redirect(http.StatusSeeOther, "/account")
	}
	token, err := a.auth.SetRegistrationToken(c.Response())
	if err != nil {
		return err
	}
	return a.renderRegistration(c, http.StatusOK, Registration{}, "", token)
}

func (a *App) register(c echo.Context) error {
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, 64<<10)
	if err := c.Request().ParseForm(); err != nil {
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "注册表单不能超过 64KB")
	}
	if !a.auth.ValidRegistrationToken(c.Request(), c.FormValue("csrf")) {
		return echo.NewHTTPError(http.StatusForbidden, "注册页面已过期，请刷新后重试")
	}
	ip := clientIP(c)
	if allowed, retry := a.registerIPRate.Allow(ip); !allowed {
		c.Response().Header().Set("Retry-After", strconv.Itoa(max(1, int(retry.Seconds()))))
		return echo.NewHTTPError(http.StatusTooManyRequests, "注册尝试过于频繁，请稍后再试")
	}
	form := Registration{
		Username: strings.ToLower(strings.TrimSpace(c.FormValue("username"))),
		Nickname: strings.TrimSpace(c.FormValue("nickname")),
		Email:    strings.TrimSpace(c.FormValue("email")),
	}
	password := c.FormValue("password")
	if err := validateRegistration(form, password, c.FormValue("password_confirm")); err != nil {
		return a.renderRegistration(c, http.StatusUnprocessableEntity, form, err.Error(), c.FormValue("csrf"))
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	user, err := a.repo.CreateUser(form.Username, string(hash), form.Nickname, form.Email)
	if err != nil {
		if isUniqueConflict(err) {
			return a.renderRegistration(c, http.StatusConflict, form, "用户名已被使用", c.FormValue("csrf"))
		}
		return err
	}
	if err := a.auth.SetSession(c.Response(), user.ID, user.PasswordHash); err != nil {
		return err
	}
	a.auth.ClearRegistrationToken(c.Response())
	if user.IsCreator {
		return c.Redirect(http.StatusSeeOther, "/admin")
	}
	return c.Redirect(http.StatusSeeOther, "/")
}

func (a *App) renderRegistration(c echo.Context, status int, form Registration, message, token string) error {
	data, err := a.baseData(c, "注册")
	if err != nil {
		return err
	}
	firstUser, err := a.repo.NeedsCreator()
	if err != nil {
		return err
	}
	data.View, data.Error, data.CSRF = "register", message, token
	data.FirstUser, data.Registration = firstUser, form
	return a.render(c, status, "auth", data)
}

func validateRegistration(form Registration, password, confirmation string) error {
	if length := len(form.Username); length < 3 || length > 32 {
		return errors.New("用户名长度应为 3 至 32 个字符")
	}
	for _, char := range form.Username {
		if !((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.') {
			return errors.New("用户名只能包含小写字母、数字、点、下划线和连字符")
		}
	}
	if length := len([]rune(form.Nickname)); length < 1 || length > 50 {
		return errors.New("昵称长度应为 1 至 50 个字符")
	}
	if len([]rune(form.Email)) > 160 {
		return errors.New("邮箱不能超过 160 个字符")
	}
	if _, err := mail.ParseAddress(form.Email); err != nil {
		return errors.New("邮箱格式不正确")
	}
	if err := validatePassword(password); err != nil {
		return err
	}
	if password != confirmation {
		return errors.New("两次输入的密码不一致")
	}
	return nil
}

func (a *App) accountPage(c echo.Context) error {
	return a.renderAccount(c, http.StatusOK, "", "")
}

func (a *App) saveAccount(c echo.Context) error {
	user := currentUser(c)
	switch c.FormValue("action") {
	case "profile":
		nickname := strings.TrimSpace(c.FormValue("nickname"))
		email := strings.TrimSpace(c.FormValue("email"))
		website := strings.TrimSpace(c.FormValue("website_url"))
		formUser := *user
		formUser.Nickname, formUser.Email, formUser.WebsiteURL = nickname, email, website
		if length := len([]rune(nickname)); length < 1 || length > 50 {
			return a.renderAccountForm(c, http.StatusUnprocessableEntity, "昵称长度应为 1 至 50 个字符", "", &formUser)
		}
		if len([]rune(email)) > 160 {
			return a.renderAccountForm(c, http.StatusUnprocessableEntity, "邮箱不能超过 160 个字符", "", &formUser)
		}
		if _, err := mail.ParseAddress(email); err != nil {
			return a.renderAccountForm(c, http.StatusUnprocessableEntity, "邮箱格式不正确", "", &formUser)
		}
		if len([]rune(website)) > 300 {
			return a.renderAccountForm(c, http.StatusUnprocessableEntity, "网站地址不能超过 300 个字符", "", &formUser)
		}
		if website != "" && !validHTTPURL(website) {
			return a.renderAccountForm(c, http.StatusUnprocessableEntity, "网站地址格式不正确", "", &formUser)
		}
		if err := a.repo.UpdateUserProfile(user.ID, nickname, email, website); err != nil {
			return err
		}
		return c.Redirect(http.StatusSeeOther, "/account?saved=profile")
	case "password":
		if !a.auth.PasswordMatches(user.PasswordHash, c.FormValue("current_password")) {
			return a.renderAccount(c, http.StatusUnprocessableEntity, "当前密码不正确", "")
		}
		password := c.FormValue("new_password")
		if err := validatePassword(password); err != nil {
			return a.renderAccount(c, http.StatusUnprocessableEntity, err.Error(), "")
		}
		if password != c.FormValue("password_confirm") {
			return a.renderAccount(c, http.StatusUnprocessableEntity, "两次输入的新密码不一致", "")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		if err := a.repo.UpdateUserPassword(user.ID, string(hash)); err != nil {
			return err
		}
		if err := a.auth.SetSession(c.Response(), user.ID, string(hash)); err != nil {
			return err
		}
		return c.Redirect(http.StatusSeeOther, "/account?saved=password")
	default:
		return echo.NewHTTPError(http.StatusBadRequest, "无效的账户操作")
	}
}

func (a *App) renderAccount(c echo.Context, status int, message, notice string) error {
	return a.renderAccountForm(c, status, message, notice, nil)
}

func (a *App) renderAccountForm(c echo.Context, status int, message, notice string, form *User) error {
	data, err := a.baseData(c, "账户设置")
	if err != nil {
		return err
	}
	user, err := a.repo.UserByID(currentUser(c).ID)
	if err != nil {
		return err
	}
	if form != nil {
		user.Nickname, user.Email, user.WebsiteURL = form.Nickname, form.Email, form.WebsiteURL
	}
	data.View, data.Error, data.User = "account", message, &user
	if notice != "" {
		data.Notice = notice
	} else if saved := c.QueryParam("saved"); saved == "profile" || saved == "password" {
		data.Notice = saved
	}
	return a.render(c, status, "public", data)
}

func (a *App) login(c echo.Context) error {
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, 64<<10)
	if err := c.Request().ParseForm(); err != nil {
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "登录表单不能超过 64KB")
	}
	username := strings.TrimSpace(c.FormValue("username"))
	if len([]rune(username)) < 1 || len([]rune(username)) > 191 {
		return echo.NewHTTPError(http.StatusUnauthorized, "用户名或密码不正确")
	}
	ipKey := clientIP(c)
	accountKey := strings.ToLower(username)
	if allowed, retry := a.loginIPRate.Allow(ipKey); !allowed {
		c.Response().Header().Set("Retry-After", strconv.Itoa(max(1, int(retry.Seconds()))))
		return echo.NewHTTPError(http.StatusTooManyRequests, "登录尝试过于频繁，请稍后再试")
	}
	if allowed, retry := a.loginAccountRate.Allow(accountKey); !allowed {
		c.Response().Header().Set("Retry-After", strconv.Itoa(max(1, int(retry.Seconds()))))
		return echo.NewHTTPError(http.StatusTooManyRequests, "登录尝试过于频繁，请稍后再试")
	}
	user, err := a.repo.UserByUsername(username)
	if err != nil || !a.auth.PasswordMatches(user.PasswordHash, c.FormValue("password")) {
		data, dataErr := a.baseData(c, "登录")
		if dataErr != nil {
			return dataErr
		}
		data.View, data.Error = "login", "用户名或密码不正确"
		return a.render(c, http.StatusUnauthorized, "auth", data)
	}
	if err := a.auth.SetSession(c.Response(), user.ID, user.PasswordHash); err != nil {
		return err
	}
	a.loginIPRate.Reset(ipKey)
	a.loginAccountRate.Reset(accountKey)
	next := c.QueryParam("next")
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		if user.IsCreator {
			next = "/admin"
		} else {
			next = "/"
		}
	}
	if !user.IsCreator && strings.HasPrefix(next, "/admin") {
		next = "/"
	}
	return c.Redirect(http.StatusSeeOther, next)
}

func (a *App) logout(c echo.Context) error {
	if claims, ok := c.Get("claims").(sessionClaims); ok && !a.auth.ValidCSRF(claims, c.FormValue("csrf")) {
		return echo.NewHTTPError(http.StatusForbidden, "请求校验失败")
	}
	a.auth.ClearSession(c.Response())
	return c.Redirect(http.StatusSeeOther, "/")
}

func (a *App) adminDashboard(c echo.Context) error {
	data, err := a.baseData(c, "管理")
	if err != nil {
		return err
	}
	data.Stats, err = a.repo.Dashboard()
	if err != nil {
		return err
	}
	data.View = "dashboard"
	return a.render(c, http.StatusOK, "admin", data)
}

func (a *App) adminPosts(c echo.Context) error {
	data, err := a.baseData(c, "内容管理")
	if err != nil {
		return err
	}
	data.Posts, err = a.repo.AdminPosts()
	if err != nil {
		return err
	}
	data.View = "posts"
	return a.render(c, http.StatusOK, "admin", data)
}

func (a *App) adminComments(c echo.Context) error {
	data, err := a.baseData(c, "评论管理")
	if err != nil {
		return err
	}
	data.AdminComments, err = a.repo.AdminComments()
	if err != nil {
		return err
	}
	data.View = "comments"
	return a.render(c, http.StatusOK, "admin", data)
}

func (a *App) moderateComment(c echo.Context) error {
	id := parseID(c.Param("id"))
	action := c.FormValue("action")
	var urls []string
	if action == "approve" {
		comment, err := a.repo.CommentNotification(id)
		if err != nil {
			return err
		}
		post := Post{Kind: comment.PostKind, Slug: comment.PostSlug}
		urls = []string{absoluteURL(c, contentURL(post)), absoluteURL(c, "/admin/comments")}
	}
	if err := a.repo.ModerateComment(id, action, urls...); err != nil {
		return err
	}
	a.wakeNotifications()
	return c.Redirect(http.StatusSeeOther, "/admin/comments")
}

func (a *App) adminNotifications(c echo.Context) error {
	data, err := a.baseData(c, "邮件通知")
	if err != nil {
		return err
	}
	data.NotificationJobs, err = a.repo.NotificationJobs()
	if err != nil {
		return err
	}
	data.View = "notifications"
	return a.render(c, http.StatusOK, "admin", data)
}

func (a *App) retryNotification(c echo.Context) error {
	if err := a.repo.RetryNotification(parseID(c.Param("id")), c.Param("kind")); err != nil {
		if IsNotFound(err) {
			return echo.NewHTTPError(http.StatusConflict, "该通知无法重试，请刷新列表")
		}
		return err
	}
	a.wakeNotifications()
	return c.Redirect(http.StatusSeeOther, "/admin/notifications")
}

func (a *App) adminCategories(c echo.Context) error {
	return a.renderAdminCategories(c, http.StatusOK, Category{}, "")
}

func (a *App) renderAdminCategories(c echo.Context, status int, category Category, message string) error {
	data, err := a.baseData(c, "分类管理")
	if err != nil {
		return err
	}
	data.Categories, err = a.repo.Categories()
	if err != nil {
		return err
	}
	data.View, data.Category, data.Error = "categories", category, message
	return a.render(c, status, "admin", data)
}

func (a *App) saveCategory(c echo.Context) error {
	sortOrder, _ := strconv.Atoi(c.FormValue("sort_order"))
	category := Category{ID: parseID(c.FormValue("id")), Name: strings.TrimSpace(c.FormValue("name")), Slug: slugify(c.FormValue("slug")), Description: strings.TrimSpace(c.FormValue("description")), SortOrder: sortOrder}
	if length := len([]rune(category.Name)); length < 1 || length > 100 {
		return a.renderAdminCategories(c, http.StatusUnprocessableEntity, category, "分类名称长度应为 1 至 100 个字符")
	}
	if category.Slug == "" {
		category.Slug = slugify(category.Name)
	}
	if category.Slug == "" {
		return a.renderAdminCategories(c, http.StatusUnprocessableEntity, category, "分类 Slug 不能为空")
	}
	if len([]rune(category.Slug)) > 180 {
		return a.renderAdminCategories(c, http.StatusUnprocessableEntity, category, "分类 Slug 不能超过 180 个字符")
	}
	if len([]rune(category.Description)) > 500 {
		return a.renderAdminCategories(c, http.StatusUnprocessableEntity, category, "分类描述不能超过 500 个字符")
	}
	if err := a.repo.SaveCategory(category); err != nil {
		if isUniqueConflict(err) {
			return a.renderAdminCategories(c, http.StatusConflict, category, "分类 Slug 已被使用")
		}
		return err
	}
	return c.Redirect(http.StatusSeeOther, "/admin/categories")
}

func (a *App) deleteCategory(c echo.Context) error {
	if err := a.repo.DeleteCategory(parseID(c.Param("id"))); err != nil {
		return err
	}
	return c.Redirect(http.StatusSeeOther, "/admin/categories")
}

func (a *App) adminLinks(c echo.Context) error {
	return a.renderAdminLinks(c, http.StatusOK, Link{}, "")
}

func (a *App) renderAdminLinks(c echo.Context, status int, link Link, message string) error {
	data, err := a.baseData(c, "友链管理")
	if err != nil {
		return err
	}
	data.Links, err = a.repo.Links()
	if err != nil {
		return err
	}
	data.View, data.Link, data.Error = "admin-links", link, message
	return a.render(c, status, "admin", data)
}

func (a *App) saveLink(c echo.Context) error {
	sortOrder, _ := strconv.Atoi(c.FormValue("sort_order"))
	link := Link{ID: parseID(c.FormValue("id")), Name: strings.TrimSpace(c.FormValue("name")), URL: strings.TrimSpace(c.FormValue("url")), IconURL: strings.TrimSpace(c.FormValue("icon_url")), Description: strings.TrimSpace(c.FormValue("description")), SortOrder: sortOrder}
	if length := len([]rune(link.Name)); length < 1 || length > 100 {
		return a.renderAdminLinks(c, http.StatusUnprocessableEntity, link, "友链名称长度应为 1 至 100 个字符")
	}
	if len([]rune(link.URL)) > 2000 || !validHTTPURL(link.URL) {
		return a.renderAdminLinks(c, http.StatusUnprocessableEntity, link, "请填写有效的 HTTP 或 HTTPS 友链地址")
	}
	if len([]rune(link.IconURL)) > 2000 {
		return a.renderAdminLinks(c, http.StatusUnprocessableEntity, link, "图标地址不能超过 2000 个字符")
	}
	if link.IconURL != "" {
		if !validHTTPURL(link.IconURL) {
			return a.renderAdminLinks(c, http.StatusUnprocessableEntity, link, "图标必须是有效的 HTTP 或 HTTPS 地址")
		}
	}
	if len([]rune(link.Description)) > 500 {
		return a.renderAdminLinks(c, http.StatusUnprocessableEntity, link, "友链描述不能超过 500 个字符")
	}
	if err := a.repo.SaveLink(link); err != nil {
		return err
	}
	return c.Redirect(http.StatusSeeOther, "/admin/links")
}

func validHTTPURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	return err == nil && parsed.Host != "" && (strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https"))
}

func (a *App) deleteLink(c echo.Context) error {
	if err := a.repo.DeleteLink(parseID(c.Param("id"))); err != nil {
		return err
	}
	return c.Redirect(http.StatusSeeOther, "/admin/links")
}

func (a *App) adminSettings(c echo.Context) error {
	data, err := a.baseData(c, "站点设置")
	if err != nil {
		return err
	}
	data.Mail, err = a.repo.MailSettings()
	if err != nil {
		return err
	}
	data.S3, err = a.repo.S3Settings()
	if err != nil {
		return err
	}
	data.AI, err = a.repo.AISettings()
	if err != nil {
		return err
	}
	data.View = "settings"
	return a.render(c, http.StatusOK, "admin", data)
}

func (a *App) saveSettings(c echo.Context) error {
	perPage, _ := strconv.Atoi(c.FormValue("posts_per_page"))
	if perPage < 1 || perPage > 100 {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "每页文章数应为 1 至 100")
	}
	values := map[string]string{
		"site_name": strings.TrimSpace(c.FormValue("site_name")), "site_tagline": strings.TrimSpace(c.FormValue("site_tagline")),
		"site_description": strings.TrimSpace(c.FormValue("site_description")), "site_keywords": strings.TrimSpace(c.FormValue("site_keywords")),
		"site_footer": strings.TrimSpace(c.FormValue("site_footer")), "footer_beian": strings.TrimSpace(c.FormValue("footer_beian")),
		"favicon_url": strings.TrimSpace(c.FormValue("favicon_url")), "posts_per_page": strconv.Itoa(perPage),
		"comments_enabled": checkboxValue(c.FormValue("comments_enabled")), "comments_notify": checkboxValue(c.FormValue("comments_notify")),
		"comments_require_approval": checkboxValue(c.FormValue("comments_require_approval")), "pretty_url": "1",
	}
	if values["site_name"] == "" {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "站点名称不能为空")
	}
	existingMail, err := a.repo.MailSettings()
	if err != nil {
		return err
	}
	mailPort, _ := strconv.Atoi(c.FormValue("smtp_port"))
	mailSettings := MailSettings{
		Enabled: c.FormValue("smtp_enabled") == "1", Encryption: strings.ToLower(strings.TrimSpace(c.FormValue("smtp_encryption"))),
		Host: strings.TrimSpace(c.FormValue("smtp_host")), Port: mailPort, Username: strings.TrimSpace(c.FormValue("smtp_username")),
		Password: c.FormValue("smtp_password"), FromEmail: strings.TrimSpace(c.FormValue("smtp_from_email")),
		FromName: strings.TrimSpace(c.FormValue("smtp_from_name")), NotifyEmail: strings.TrimSpace(c.FormValue("smtp_notify_email")),
	}
	if mailSettings.Password == "" {
		mailSettings.Password = existingMail.Password
	}
	if err := validateMailSettings(mailSettings); err != nil {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, err.Error())
	}
	existingS3, err := a.repo.S3Settings()
	if err != nil {
		return err
	}
	prefix, err := normalizeS3Prefix(c.FormValue("s3_path_prefix"))
	if err != nil {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, err.Error())
	}
	s3Settings := S3Settings{
		Enabled: c.FormValue("s3_enabled") == "1", Endpoint: strings.TrimRight(strings.TrimSpace(c.FormValue("s3_endpoint")), "/"),
		Region: strings.TrimSpace(c.FormValue("s3_region")), Bucket: strings.TrimSpace(c.FormValue("s3_bucket")),
		AccessKey: strings.TrimSpace(c.FormValue("s3_access_key")), SecretKey: c.FormValue("s3_secret_key"),
		PublicURL: strings.TrimRight(strings.TrimSpace(c.FormValue("s3_public_url")), "/"), PathPrefix: prefix,
		PathStyle: c.FormValue("s3_path_style") == "1", KeepLocal: c.FormValue("s3_keep_local") == "1",
	}
	if s3Settings.AccessKey == "" {
		s3Settings.AccessKey = existingS3.AccessKey
	}
	if s3Settings.SecretKey == "" {
		s3Settings.SecretKey = existingS3.SecretKey
	}
	if err := validateS3Settings(s3Settings); err != nil {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, err.Error())
	}
	existingAI, err := a.repo.AISettings()
	if err != nil {
		return err
	}
	aiSettings := AISettings{
		APIURL: strings.TrimRight(strings.TrimSpace(c.FormValue("ai_api_url")), "/"),
		APIKey: c.FormValue("ai_api_key"), Model: strings.TrimSpace(c.FormValue("ai_model")),
		SummaryPrompt: strings.TrimSpace(c.FormValue("ai_summary_prompt")),
		SlugPrompt:    strings.TrimSpace(c.FormValue("ai_slug_prompt")),
		PolishPrompt:  strings.TrimSpace(c.FormValue("ai_polish_prompt")),
	}
	if aiSettings.APIKey == "" {
		aiSettings.APIKey = existingAI.APIKey
	}
	if err := validateAISettings(aiSettings); err != nil {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, err.Error())
	}
	if err := a.repo.SaveAdminSettings(values, mailSettings, s3Settings, aiSettings); err != nil {
		return err
	}
	return c.Redirect(http.StatusSeeOther, "/admin/settings?saved=1")
}

func checkboxValue(value string) string {
	if value == "1" {
		return "1"
	}
	return "0"
}

func (a *App) uploadAttachment(c echo.Context) error {
	request := c.Request()
	request.Body = http.MaxBytesReader(c.Response(), request.Body, 12<<20)
	file, header, err := request.FormFile("file")
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "请选择不超过 10MB 的文件")
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, (10<<20)+1))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "无法读取文件")
	}
	if len(data) > 10<<20 {
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "文件不能超过 10MB")
	}
	sample := data
	if len(sample) > 512 {
		sample = sample[:512]
	}
	mime := http.DetectContentType(sample)
	extensions := map[string]string{
		"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp",
		"application/pdf": ".pdf", "text/plain; charset=utf-8": ".txt",
	}
	ext, ok := extensions[mime]
	if !ok {
		return echo.NewHTTPError(http.StatusUnsupportedMediaType, "仅支持 JPG、PNG、GIF、WebP、PDF 和纯文本")
	}
	token, err := randomToken(12)
	if err != nil {
		return err
	}
	year := strconv.Itoa(time.Now().Year())
	name := token + ext
	s3Settings, err := a.repo.S3Settings()
	if err != nil {
		return err
	}
	if s3Settings.Enabled {
		if err := validateS3Settings(s3Settings); err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "S3 配置无效："+err.Error())
		}
		if s3Settings.KeepLocal {
			if err := writeLocalUpload(a.cfg.UploadDir, year, name, data); err != nil {
				return err
			}
		}
		key := path.Join(s3Settings.PathPrefix, year, name)
		ctx, cancel := context.WithTimeout(c.Request().Context(), 30*time.Second)
		defer cancel()
		if err := a.putObject(ctx, s3Settings, key, mime, bytes.NewReader(data), int64(len(data))); err != nil {
			return echo.NewHTTPError(http.StatusBadGateway, "上传到 S3 失败").SetInternal(err)
		}
		return c.JSON(http.StatusCreated, map[string]string{"url": s3Settings.objectURL(key), "name": header.Filename})
	}
	if err := writeLocalUpload(a.cfg.UploadDir, year, name, data); err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, map[string]string{"url": "/uploads/" + year + "/" + name, "name": header.Filename})
}

func writeLocalUpload(root, year, name string, data []byte) error {
	directory := filepath.Join(root, year)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(directory, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func (a *App) aiAction(c echo.Context) error {
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, 2<<20)
	if err := c.Request().ParseMultipartForm(2 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		var maxBytesError *http.MaxBytesError
		if !errors.As(err, &maxBytesError) {
			return echo.NewHTTPError(http.StatusBadRequest, "AI 请求表单格式无效")
		}
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "AI 请求不能超过 2MB")
	}
	user := currentUser(c)
	if user == nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	if allowed, retry := a.aiRate.Allow(strconv.FormatInt(user.ID, 10)); !allowed {
		c.Response().Header().Set("Retry-After", strconv.Itoa(max(1, int(retry.Seconds()))))
		return echo.NewHTTPError(http.StatusTooManyRequests, "AI 操作过于频繁，请稍后再试")
	}
	settings, err := a.repo.AISettings()
	if err != nil {
		return err
	}
	if !settings.Ready() {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "请先在设置中配置 AI")
	}
	if err := validateAISettings(settings); err != nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, err.Error())
	}
	action := c.FormValue("action")
	title := strings.TrimSpace(c.FormValue("title"))
	content := strings.TrimSpace(c.FormValue("content"))
	if len([]rune(title)) > 200 || len([]rune(content)) > 300000 {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "AI 输入内容过长")
	}

	var systemPrompt, userPrompt string
	maxTokens := 1000
	switch action {
	case "summary":
		if content == "" {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "请先填写正文")
		}
		systemPrompt = settings.SummaryPrompt
		userPrompt = "文章标题：" + title + "\n\n文章正文：\n" + content
		maxTokens = 500
	case "slug":
		if title == "" {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "请先填写标题")
		}
		systemPrompt, userPrompt, maxTokens = settings.SlugPrompt, title, 100
	case "polish":
		if content == "" {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "请先填写正文或选择需要润色的内容")
		}
		instruction := strings.TrimSpace(c.FormValue("instruction"))
		if len([]rune(instruction)) > 500 {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "润色要求不能超过 500 个字符")
		}
		systemPrompt = settings.PolishPrompt
		if instruction != "" {
			systemPrompt += "\n\n本次润色要求：" + instruction
		}
		userPrompt, maxTokens = content, 8192
	default:
		return echo.NewHTTPError(http.StatusBadRequest, "无效的 AI 操作")
	}

	result, err := a.completeAI(c.Request().Context(), a.aiClient, settings, systemPrompt, userPrompt, maxTokens)
	if err != nil {
		a.logger.Warn("AI request failed", "action", action, "error", err)
		return echo.NewHTTPError(http.StatusBadGateway, err.Error())
	}
	switch action {
	case "summary":
		result = truncateRunes(strings.Trim(strings.TrimSpace(result), "\"'"), 500)
	case "slug":
		result = normalizeAISlug(result)
		if result == "" {
			return echo.NewHTTPError(http.StatusBadGateway, "AI 未返回有效的 Slug")
		}
	case "polish":
		if len([]rune(result)) > 300000 {
			return echo.NewHTTPError(http.StatusBadGateway, "AI 返回的润色正文过长")
		}
	}
	return c.JSON(http.StatusOK, map[string]string{"result": result})
}

func (a *App) editor(c echo.Context) error {
	data, err := a.editorPageData(c)
	if err != nil {
		return err
	}
	if id := parseID(c.Param("id")); id > 0 {
		data.Post, err = a.repo.FindPostByID(id)
		if IsNotFound(err) {
			return echo.NewHTTPError(http.StatusNotFound, "内容不存在")
		}
		if err != nil {
			return err
		}
	} else {
		data.Post = Post{Kind: "post", Status: "draft", AllowComments: true, AuthorID: data.User.ID}
	}
	data.View = "editor"
	return a.render(c, http.StatusOK, "admin", data)
}

func (a *App) editorPageData(c echo.Context) (PageData, error) {
	data, err := a.baseData(c, "写作")
	if err != nil {
		return data, err
	}
	data.Categories, err = a.repo.Categories()
	if err != nil {
		return data, err
	}
	aiSettings, err := a.repo.AISettings()
	if err != nil {
		return data, err
	}
	data.AIReady = aiSettings.Ready()
	data.View = "editor"
	return data, nil
}

func (a *App) renderEditorError(c echo.Context, status int, post Post, message string) error {
	data, err := a.editorPageData(c)
	if err != nil {
		return err
	}
	data.Post, data.Error = post, message
	return a.render(c, status, "admin", data)
}

func (a *App) saveEditor(c echo.Context) error {
	id := parseID(c.Param("id"))
	post := Post{
		ID: id, AuthorID: currentUser(c).ID, CategoryID: parseID(c.FormValue("category_id")),
		Title: strings.TrimSpace(c.FormValue("title")), Slug: slugify(c.FormValue("slug")),
		Excerpt: strings.TrimSpace(c.FormValue("excerpt")), Content: c.FormValue("content"),
		Kind: c.FormValue("kind"), Status: c.FormValue("status"), Tags: ParseTags(c.FormValue("tags")),
		Pinned: c.FormValue("is_pinned") == "1", AllowComments: c.FormValue("allow_comments") == "1",
	}
	if post.Slug == "" {
		post.Slug = slugify(post.Title)
	}
	if id > 0 {
		existing, err := a.repo.FindPostByID(id)
		if err != nil {
			return err
		}
		post.PublishedAt, post.CreatedAt, post.Views = existing.PublishedAt, existing.CreatedAt, existing.Views
	}
	if err := ValidatePost(post); err != nil {
		return a.renderEditorError(c, http.StatusUnprocessableEntity, post, err.Error())
	}
	postID, err := a.repo.SavePost(post)
	if err != nil {
		if isUniqueConflict(err) {
			return a.renderEditorError(c, http.StatusConflict, post, "Slug 已被其他内容使用")
		}
		return err
	}
	return c.Redirect(http.StatusSeeOther, "/admin/edit/"+strconv.FormatInt(postID, 10)+"?saved=1")
}

func (a *App) deletePost(c echo.Context) error {
	if err := a.repo.DeletePost(parseID(c.Param("id"))); err != nil {
		return err
	}
	return c.Redirect(http.StatusSeeOther, "/admin/posts")
}

func (a *App) rss(c echo.Context) error {
	settings, err := a.repo.Settings()
	if err != nil {
		return err
	}
	posts, err := a.repo.ListPublished(20, 0)
	if err != nil {
		return err
	}
	feed := rssFeed{Version: "2.0", Channel: rssChannel{Title: settings.Name, Link: absoluteURL(c, "/"), Description: settings.Description}}
	for _, post := range posts {
		feed.Channel.Items = append(feed.Channel.Items, rssItem{Title: post.Title, Link: absoluteURL(c, contentURL(post)), GUID: absoluteURL(c, contentURL(post)), PubDate: post.PublishedTime().Format(time.RFC1123Z), Description: DeriveExcerpt(post, 240)})
	}
	output, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		return err
	}
	return c.Blob(http.StatusOK, "application/rss+xml; charset=utf-8", append([]byte(xml.Header), output...))
}

func (a *App) sitemap(c echo.Context) error {
	contents, err := a.repo.SitemapContents()
	if err != nil {
		return err
	}
	set := sitemapSet{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9"}
	for _, path := range []string{"/", "/archives", "/tags", "/links"} {
		set.URLs = append(set.URLs, sitemapURL{Loc: absoluteURL(c, path)})
	}
	for _, post := range contents {
		set.URLs = append(set.URLs, sitemapURL{Loc: absoluteURL(c, contentURL(post)), LastMod: time.Unix(post.UpdatedAt, 0).Format("2006-01-02")})
	}
	output, err := xml.MarshalIndent(set, "", "  ")
	if err != nil {
		return err
	}
	return c.Blob(http.StatusOK, "application/xml; charset=utf-8", append([]byte(xml.Header), output...))
}

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}
type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Items       []rssItem `xml:"item"`
}
type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description"`
}
type sitemapSet struct {
	XMLName xml.Name     `xml:"urlset"`
	XMLNS   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}
type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

func contentURL(post Post) string {
	if post.Kind == "page" {
		return "/" + url.PathEscape(post.Slug)
	}
	return "/archive/" + url.PathEscape(post.Slug)
}

func absoluteURL(c echo.Context, path string) string {
	scheme := c.Scheme()
	if scheme == "" {
		scheme = "http"
	}
	return scheme + "://" + c.Request().Host + path
}

func clientIP(c echo.Context) string { return c.RealIP() }

func countComments(comments []Comment) int {
	total := 0
	for _, comment := range comments {
		total += 1 + countComments(comment.Children)
	}
	return total
}

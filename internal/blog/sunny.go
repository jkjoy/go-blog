package blog

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/labstack/echo/v4"
)

func (a *App) loadPublicData(data *PageData) error {
	var err error
	if data.SiteOwner, err = a.repo.SiteOwner(); err != nil {
		return err
	}
	if data.SidebarTags, err = a.repo.Tags(); err != nil {
		return err
	}
	if len(data.SidebarTags) > 20 {
		data.SidebarTags = data.SidebarTags[:20]
	}
	categories, err := a.repo.Categories()
	if err != nil {
		return err
	}
	for _, category := range categories {
		if category.Count > 0 {
			data.SidebarCategories = append(data.SidebarCategories, category)
		}
	}
	data.RecentComments, err = a.repo.RecentPublicComments()
	return err
}

func (a *App) search(c echo.Context) error {
	// Keep pre-existing /search pages reachable; submitted searches always have q.
	if params := c.QueryParams(); !params.Has("q") && !params.Has("page") {
		if _, err := a.repo.FindContent("page", "search", currentUserIsCreator(c)); err == nil {
			c.SetParamNames("slug")
			c.SetParamValues("search")
			return a.page(c)
		} else if !IsNotFound(err) {
			return err
		}
	}
	query := strings.TrimSpace(c.QueryParam("q"))
	if !utf8.ValidString(query) || utf8.RuneCountInString(query) > 200 {
		return echo.NewHTTPError(http.StatusBadRequest, "搜索关键词不能超过 200 个字符")
	}
	page := 1
	if rawPage := c.QueryParam("page"); rawPage != "" {
		var err error
		page, err = strconv.Atoi(rawPage)
		if err != nil || page < 1 {
			return echo.NewHTTPError(http.StatusBadRequest, "无效的搜索页码")
		}
	}
	data, err := a.baseData(c, "搜索")
	if err != nil {
		return err
	}
	data.View, data.SearchQuery, data.Page, data.TotalPages = "search", query, page, 1
	perPage := data.Settings.PostsPerPage
	if page-1 > int(^uint(0)>>1)/perPage {
		return echo.NewHTTPError(http.StatusNotFound, "页面不存在")
	}
	if query != "" {
		data.Posts, data.Total, err = a.repo.SearchPublished(query, perPage, (page-1)*perPage)
		if err != nil {
			return err
		}
		data.TotalPages = max(1, (data.Total+perPage-1)/perPage)
		a.decoratePosts(data.Posts)
	}
	if page > data.TotalPages {
		return echo.NewHTTPError(http.StatusNotFound, "页面不存在")
	}
	return a.render(c, http.StatusOK, "public", data)
}

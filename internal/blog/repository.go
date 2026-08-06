package blog

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"
)

type Repository struct{ db *Database }

func NewRepository(db *Database) *Repository { return &Repository{db: db} }

func (r *Repository) Settings() (SiteSettings, error) {
	rows, err := r.db.Query("SELECT name,value FROM settings")
	if err != nil {
		return SiteSettings{}, err
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return SiteSettings{}, err
		}
		values[name] = value
	}
	perPage, _ := strconv.Atoi(values["posts_per_page"])
	if perPage < 1 || perPage > 100 {
		perPage = 6
	}
	return SiteSettings{
		Name: values["site_name"], Tagline: values["site_tagline"],
		Description: values["site_description"], Keywords: values["site_keywords"],
		Footer: values["site_footer"], Beian: values["footer_beian"],
		FaviconURL: values["favicon_url"], PostsPerPage: perPage,
		Comments: values["comments_enabled"] != "0", RequireApproval: values["comments_require_approval"] != "0",
		PrettyURL: values["pretty_url"] != "0",
	}, rows.Err()
}

const postColumns = `p.id,COALESCE(p.author_id,0),COALESCE(p.category_id,0),p.slug,p.title,p.excerpt,p.content,
p.kind,p.tags,p.views,p.status,p.published_at,p.created_at,p.updated_at,p.is_pinned,p.allow_comments,
COALESCE(NULLIF(u.nickname,''),u.username,''),COALESCE(c.name,''),COALESCE(c.slug,'')`

func scanPost(scanner interface{ Scan(...any) error }) (Post, error) {
	var post Post
	var pinned, comments int
	err := scanner.Scan(
		&post.ID, &post.AuthorID, &post.CategoryID, &post.Slug, &post.Title, &post.Excerpt, &post.Content,
		&post.Kind, &post.TagsJSON, &post.Views, &post.Status, &post.PublishedAt, &post.CreatedAt, &post.UpdatedAt,
		&pinned, &comments, &post.AuthorName, &post.CategoryName, &post.CategorySlug,
	)
	post.Pinned = pinned != 0
	post.AllowComments = comments != 0
	return post, err
}

func (r *Repository) ListPublished(limit, offset int) ([]Post, error) {
	query := `SELECT ` + postColumns + ` FROM posts p
LEFT JOIN users u ON u.id=p.author_id LEFT JOIN categories c ON c.id=p.category_id
WHERE p.kind='post' AND p.status='published' AND p.published_at<=?
ORDER BY p.is_pinned DESC,p.published_at DESC,p.id DESC LIMIT ? OFFSET ?`
	rows, err := r.db.Query(query, time.Now().Unix(), limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return r.collectPosts(rows)
}

func (r *Repository) AllPublished() ([]Post, error) {
	query := `SELECT ` + postColumns + ` FROM posts p
LEFT JOIN users u ON u.id=p.author_id LEFT JOIN categories c ON c.id=p.category_id
WHERE p.kind='post' AND p.status='published' AND p.published_at<=?
ORDER BY p.published_at DESC,p.id DESC`
	rows, err := r.db.Query(query, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return r.collectPosts(rows)
}

func (r *Repository) collectPosts(rows *sql.Rows) ([]Post, error) {
	posts := []Post{}
	for rows.Next() {
		post, err := scanPost(rows)
		if err != nil {
			return nil, err
		}
		posts = append(posts, post)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return r.HydrateTags(posts)
}

func (r *Repository) CountPublished() (int, error) {
	var count int
	err := r.db.QueryRow("SELECT COUNT(*) FROM posts WHERE kind='post' AND status='published' AND published_at<=?", time.Now().Unix()).Scan(&count)
	return count, err
}

func (r *Repository) FindContent(kind, slug string, includeDraft bool) (Post, error) {
	query := `SELECT ` + postColumns + ` FROM posts p
LEFT JOIN users u ON u.id=p.author_id LEFT JOIN categories c ON c.id=p.category_id
WHERE p.kind=? AND p.slug=?`
	args := []any{kind, slug}
	if !includeDraft {
		query += " AND p.status='published' AND p.published_at<=?"
		args = append(args, time.Now().Unix())
	}
	post, err := scanPost(r.db.QueryRow(query, args...))
	if err != nil {
		return Post{}, err
	}
	posts, err := r.HydrateTags([]Post{post})
	if err != nil {
		return Post{}, err
	}
	return posts[0], nil
}

func (r *Repository) FindPostByID(id int64) (Post, error) {
	query := `SELECT ` + postColumns + ` FROM posts p
LEFT JOIN users u ON u.id=p.author_id LEFT JOIN categories c ON c.id=p.category_id WHERE p.id=?`
	post, err := scanPost(r.db.QueryRow(query, id))
	if err != nil {
		return Post{}, err
	}
	posts, err := r.HydrateTags([]Post{post})
	if err != nil {
		return Post{}, err
	}
	return posts[0], nil
}

func (r *Repository) NavPages() ([]Post, error) {
	query := `SELECT ` + postColumns + ` FROM posts p
LEFT JOIN users u ON u.id=p.author_id LEFT JOIN categories c ON c.id=p.category_id
WHERE p.kind='page' AND p.status='published' AND p.published_at<=? ORDER BY p.created_at,p.id`
	rows, err := r.db.Query(query, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return r.collectPosts(rows)
}

func (r *Repository) Categories() ([]Category, error) {
	rows, err := r.db.Query(`SELECT c.id,c.name,c.slug,c.description,c.sort_order,
COUNT(CASE WHEN p.kind='post' AND p.status='published' AND p.published_at<=? THEN 1 END)
FROM categories c LEFT JOIN posts p ON p.category_id=c.id
GROUP BY c.id,c.name,c.slug,c.description,c.sort_order ORDER BY c.sort_order,c.id DESC`, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Category{}
	for rows.Next() {
		var item Category
		if err := rows.Scan(&item.ID, &item.Name, &item.Slug, &item.Description, &item.SortOrder, &item.Count); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) Links() ([]Link, error) {
	rows, err := r.db.Query("SELECT id,name,url,icon_url,description,sort_order FROM links ORDER BY sort_order,id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Link{}
	for rows.Next() {
		var item Link
		if err := rows.Scan(&item.ID, &item.Name, &item.URL, &item.IconURL, &item.Description, &item.SortOrder); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) HydrateTags(posts []Post) ([]Post, error) {
	rows, err := r.db.Query("SELECT label,slug FROM tag_meta")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	slugs := map[string]string{}
	for rows.Next() {
		var label, slug string
		if err := rows.Scan(&label, &slug); err != nil {
			return nil, err
		}
		slugs[label] = slug
	}
	for index := range posts {
		var labels []string
		if err := json.Unmarshal([]byte(posts[index].TagsJSON), &labels); err != nil {
			labels = nil
		}
		for _, label := range labels {
			slug := slugs[label]
			if slug == "" {
				slug = slugify(label)
			}
			posts[index].Tags = append(posts[index].Tags, Tag{Label: label, Slug: slug})
		}
	}
	return posts, rows.Err()
}

func (r *Repository) Tags() ([]Tag, error) {
	posts, err := r.AllPublished()
	if err != nil {
		return nil, err
	}
	index := map[string]Tag{}
	for _, post := range posts {
		for _, tag := range post.Tags {
			item := index[tag.Label]
			item.Label, item.Slug, item.Count = tag.Label, tag.Slug, item.Count+1
			index[tag.Label] = item
		}
	}
	items := make([]Tag, 0, len(index))
	for _, tag := range index {
		items = append(items, tag)
	}
	sortTags(items)
	return items, nil
}

func (r *Repository) PostsByTag(slug string) ([]Post, string, error) {
	posts, err := r.AllPublished()
	if err != nil {
		return nil, "", err
	}
	filtered := []Post{}
	label := ""
	for _, post := range posts {
		for _, tag := range post.Tags {
			if tag.Slug == slug {
				label = tag.Label
				filtered = append(filtered, post)
				break
			}
		}
	}
	return filtered, label, nil
}

func (r *Repository) PostsByCategory(slug string) ([]Post, Category, error) {
	var category Category
	err := r.db.QueryRow("SELECT id,name,slug,description,sort_order FROM categories WHERE slug=?", slug).
		Scan(&category.ID, &category.Name, &category.Slug, &category.Description, &category.SortOrder)
	if err != nil {
		return nil, category, err
	}
	query := `SELECT ` + postColumns + ` FROM posts p
LEFT JOIN users u ON u.id=p.author_id LEFT JOIN categories c ON c.id=p.category_id
WHERE p.kind='post' AND p.category_id=? AND p.status='published' AND p.published_at<=?
ORDER BY p.is_pinned DESC,p.published_at DESC,p.id DESC`
	rows, err := r.db.Query(query, category.ID, time.Now().Unix())
	if err != nil {
		return nil, category, err
	}
	defer rows.Close()
	posts, err := r.collectPosts(rows)
	category.Count = len(posts)
	return posts, category, err
}

func (r *Repository) Comments(postID int64, approvedOnly bool) ([]Comment, error) {
	query := `SELECT id,post_id,COALESCE(user_id,0),COALESCE(parent_id,0),reply_to_name,
author_name,author_email,author_url,content,status,is_read,created_at FROM comments WHERE post_id=?`
	if approvedOnly {
		query += " AND status='approved'"
	}
	query += " ORDER BY created_at,id"
	rows, err := r.db.Query(query, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	flat := []Comment{}
	for rows.Next() {
		var item Comment
		var read int
		if err := rows.Scan(&item.ID, &item.PostID, &item.UserID, &item.ParentID, &item.ReplyToName,
			&item.AuthorName, &item.AuthorEmail, &item.AuthorURL, &item.Content, &item.Status, &read, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.IsRead = read != 0
		flat = append(flat, item)
	}
	return nestComments(flat), rows.Err()
}

func (r *Repository) CreateComment(comment Comment, ipHash, ip, userAgent string, requireApproval bool) error {
	status := "approved"
	if requireApproval {
		status = "pending"
	}
	now := time.Now().Unix()
	_, err := r.db.Exec(`INSERT INTO comments(post_id,user_id,parent_id,reply_to_name,author_name,author_email,
author_url,content,status,is_read,ip_hash,ip_address,user_agent,reply_notified_at,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,0,?,?,?,0,?,?)`, comment.PostID, nullableID(comment.UserID), nullableID(comment.ParentID),
		comment.ReplyToName, comment.AuthorName, comment.AuthorEmail, comment.AuthorURL, comment.Content,
		status, ipHash, ip, userAgent, now, now)
	return err
}

func (r *Repository) UserByUsername(username string) (User, error) {
	var user User
	err := r.db.QueryRow(`SELECT id,username,password_hash,nickname,email,avatar_url,website_url,signature,created_at
FROM users WHERE username=?`, username).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Nickname,
		&user.Email, &user.AvatarURL, &user.WebsiteURL, &user.Signature, &user.CreatedAt)
	return user, err
}

func (r *Repository) UserByID(id int64) (User, error) {
	var user User
	err := r.db.QueryRow(`SELECT id,username,password_hash,nickname,email,avatar_url,website_url,signature,created_at
FROM users WHERE id=?`, id).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Nickname,
		&user.Email, &user.AvatarURL, &user.WebsiteURL, &user.Signature, &user.CreatedAt)
	return user, err
}

func (r *Repository) TrackView(postID int64, ipHash string) error {
	now := time.Now().Unix()
	var insert string
	switch r.db.Dialect {
	case "postgres":
		insert = "INSERT INTO post_views(post_id,ip_hash,created_at) VALUES(?,?,?) ON CONFLICT(post_id,ip_hash) DO NOTHING"
	case "mysql":
		insert = "INSERT IGNORE INTO post_views(post_id,ip_hash,created_at) VALUES(?,?,?)"
	default:
		insert = "INSERT OR IGNORE INTO post_views(post_id,ip_hash,created_at) VALUES(?,?,?)"
	}
	result, err := r.db.Exec(insert, postID, ipHash, now)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected > 0 {
		_, err = r.db.Exec("UPDATE posts SET views=views+1 WHERE id=?", postID)
	}
	return err
}

func (r *Repository) Dashboard() (DashboardStats, error) {
	var stats DashboardStats
	queries := []struct {
		query string
		dest  any
	}{
		{"SELECT COUNT(*) FROM posts WHERE kind='post'", &stats.Posts},
		{"SELECT COUNT(*) FROM posts WHERE kind='page'", &stats.Pages},
		{"SELECT COUNT(*) FROM comments", &stats.Comments},
		{"SELECT COUNT(*) FROM comments WHERE status='pending'", &stats.PendingComments},
		{"SELECT COALESCE(SUM(views),0) FROM posts", &stats.Views},
	}
	for _, query := range queries {
		if err := r.db.QueryRow(query.query).Scan(query.dest); err != nil {
			return stats, err
		}
	}
	return stats, nil
}

func (r *Repository) AdminPosts() ([]Post, error) {
	query := `SELECT ` + postColumns + ` FROM posts p
LEFT JOIN users u ON u.id=p.author_id LEFT JOIN categories c ON c.id=p.category_id
ORDER BY p.updated_at DESC,p.id DESC`
	rows, err := r.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return r.collectPosts(rows)
}

func (r *Repository) SavePost(post Post) (int64, error) {
	now := time.Now().Unix()
	if post.PublishedAt == 0 && post.Status == "published" {
		post.PublishedAt = now
	}
	tags, _ := json.Marshal(tagLabels(post.Tags))
	if post.ID == 0 {
		if r.db.Dialect == "postgres" {
			var id int64
			err := r.db.QueryRow(`INSERT INTO posts(author_id,category_id,slug,title,excerpt,content,kind,tags,views,status,
published_at,created_at,updated_at,is_pinned,allow_comments) VALUES(?,?,?,?,?,?,?,?,0,?,?,?,?,?,?) RETURNING id`,
				post.AuthorID, nullableID(post.CategoryID), post.Slug, post.Title, post.Excerpt, post.Content, post.Kind,
				string(tags), post.Status, post.PublishedAt, now, now, boolInt(post.Pinned), boolInt(post.AllowComments)).Scan(&id)
			return id, err
		}
		result, err := r.db.Exec(`INSERT INTO posts(author_id,category_id,slug,title,excerpt,content,kind,tags,views,status,
published_at,created_at,updated_at,is_pinned,allow_comments) VALUES(?,?,?,?,?,?,?,?,0,?,?,?,?,?,?)`,
			post.AuthorID, nullableID(post.CategoryID), post.Slug, post.Title, post.Excerpt, post.Content, post.Kind,
			string(tags), post.Status, post.PublishedAt, now, now, boolInt(post.Pinned), boolInt(post.AllowComments))
		if err != nil {
			return 0, err
		}
		return result.LastInsertId()
	}
	_, err := r.db.Exec(`UPDATE posts SET author_id=?,category_id=?,slug=?,title=?,excerpt=?,content=?,kind=?,tags=?,
status=?,published_at=?,updated_at=?,is_pinned=?,allow_comments=? WHERE id=?`, post.AuthorID, nullableID(post.CategoryID),
		post.Slug, post.Title, post.Excerpt, post.Content, post.Kind, string(tags), post.Status, post.PublishedAt,
		now, boolInt(post.Pinned), boolInt(post.AllowComments), post.ID)
	return post.ID, err
}

func (r *Repository) DeletePost(id int64) error {
	_, err := r.db.Exec("DELETE FROM posts WHERE id=?", id)
	return err
}

func (r *Repository) AdminComments() ([]AdminComment, error) {
	rows, err := r.db.Query(`SELECT c.id,c.post_id,COALESCE(c.user_id,0),COALESCE(c.parent_id,0),c.reply_to_name,
c.author_name,c.author_email,c.author_url,c.content,c.status,c.is_read,c.created_at,p.title,p.slug
FROM comments c INNER JOIN posts p ON p.id=c.post_id ORDER BY c.created_at DESC,c.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AdminComment{}
	for rows.Next() {
		var item AdminComment
		var read int
		if err := rows.Scan(&item.ID, &item.PostID, &item.UserID, &item.ParentID, &item.ReplyToName,
			&item.AuthorName, &item.AuthorEmail, &item.AuthorURL, &item.Content, &item.Status, &read, &item.CreatedAt,
			&item.PostTitle, &item.PostSlug); err != nil {
			return nil, err
		}
		item.IsRead = read != 0
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) ModerateComment(id int64, action string) error {
	switch action {
	case "approve":
		_, err := r.db.Exec("UPDATE comments SET status='approved',is_read=1,updated_at=? WHERE id=?", time.Now().Unix(), id)
		return err
	case "pending":
		_, err := r.db.Exec("UPDATE comments SET status='pending',updated_at=? WHERE id=?", time.Now().Unix(), id)
		return err
	case "delete":
		_, err := r.db.Exec("DELETE FROM comments WHERE id=?", id)
		return err
	default:
		return errors.New("无效的评论操作")
	}
}

func (r *Repository) SavePublicSettings(values map[string]string) error {
	allowed := map[string]bool{
		"site_name": true, "site_tagline": true, "site_description": true, "site_keywords": true,
		"site_footer": true, "footer_beian": true, "favicon_url": true, "posts_per_page": true,
		"comments_enabled": true, "comments_require_approval": true, "pretty_url": true,
	}
	for name, value := range values {
		if !allowed[name] {
			continue
		}
		result, err := r.db.Exec("UPDATE settings SET value=? WHERE name=?", value, name)
		if err != nil {
			return err
		}
		affected, _ := result.RowsAffected()
		if affected == 0 {
			if _, err := r.db.Exec("INSERT INTO settings(name,value) VALUES(?,?)", name, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Repository) SaveCategory(category Category) error {
	now := time.Now().Unix()
	if category.ID == 0 {
		_, err := r.db.Exec("INSERT INTO categories(name,slug,description,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?)",
			category.Name, category.Slug, category.Description, category.SortOrder, now, now)
		return err
	}
	_, err := r.db.Exec("UPDATE categories SET name=?,slug=?,description=?,sort_order=?,updated_at=? WHERE id=?",
		category.Name, category.Slug, category.Description, category.SortOrder, now, category.ID)
	return err
}

func (r *Repository) DeleteCategory(id int64) error {
	tx, err := r.db.DB.Begin()
	if err != nil {
		return err
	}
	if _, err = tx.Exec(r.db.Bind("UPDATE posts SET category_id=NULL WHERE category_id=?"), id); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.Exec(r.db.Bind("DELETE FROM categories WHERE id=?"), id); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (r *Repository) SaveLink(link Link) error {
	now := time.Now().Unix()
	if link.ID == 0 {
		_, err := r.db.Exec("INSERT INTO links(name,url,icon_url,description,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?,?)",
			link.Name, link.URL, link.IconURL, link.Description, link.SortOrder, now, now)
		return err
	}
	_, err := r.db.Exec("UPDATE links SET name=?,url=?,icon_url=?,description=?,sort_order=?,updated_at=? WHERE id=?",
		link.Name, link.URL, link.IconURL, link.Description, link.SortOrder, now, link.ID)
	return err
}

func (r *Repository) DeleteLink(id int64) error {
	_, err := r.db.Exec("DELETE FROM links WHERE id=?", id)
	return err
}

func (r *Repository) CommentApprovalRequired() bool {
	var value string
	if err := r.db.QueryRow("SELECT value FROM settings WHERE name='comments_require_approval'").Scan(&value); err != nil {
		return true
	}
	return value != "0"
}

func (r *Repository) Setting(name string) string {
	var value string
	_ = r.db.QueryRow("SELECT value FROM settings WHERE name=?", name).Scan(&value)
	return value
}

func nestComments(flat []Comment) []Comment {
	children := map[int64][]Comment{}
	for _, item := range flat {
		children[item.ParentID] = append(children[item.ParentID], item)
	}
	var attach func(Comment) Comment
	attach = func(item Comment) Comment {
		for _, child := range children[item.ID] {
			item.Children = append(item.Children, attach(child))
		}
		return item
	}
	roots := []Comment{}
	for _, item := range children[0] {
		roots = append(roots, attach(item))
	}
	return roots
}

func nullableID(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func tagLabels(tags []Tag) []string {
	labels := make([]string, 0, len(tags))
	for _, tag := range tags {
		if label := strings.TrimSpace(tag.Label); label != "" {
			labels = append(labels, label)
		}
	}
	return labels
}

func slugify(value string) string {
	value = norm.NFC.String(strings.TrimSpace(strings.ToLower(value)))
	value = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127 {
			return r
		}
		return '-'
	}, value)
	return strings.Trim(strings.Join(strings.FieldsFunc(value, func(r rune) bool { return r == '-' }), "-"), "-")
}

func sortTags(items []Tag) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && (items[j].Count > items[j-1].Count ||
			(items[j].Count == items[j-1].Count && items[j].Label < items[j-1].Label)); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }

func ParseTags(raw string) []Tag {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '，' || r == '\n' })
	seen := map[string]bool{}
	tags := []Tag{}
	for _, part := range parts {
		label := strings.TrimSpace(part)
		if label != "" && !seen[label] {
			seen[label] = true
			tags = append(tags, Tag{Label: label, Slug: slugify(label)})
		}
	}
	return tags
}

func ValidatePost(post Post) error {
	if strings.TrimSpace(post.Title) == "" {
		return errors.New("标题不能为空")
	}
	if strings.TrimSpace(post.Slug) == "" {
		return errors.New("Slug 不能为空")
	}
	if post.Kind != "post" && post.Kind != "page" {
		return fmt.Errorf("无效内容类型 %q", post.Kind)
	}
	if post.Status != "draft" && post.Status != "published" {
		return fmt.Errorf("无效状态 %q", post.Status)
	}
	return nil
}

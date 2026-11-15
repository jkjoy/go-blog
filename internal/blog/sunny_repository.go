package blog

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (r *Repository) SiteOwner() (PublicProfile, error) {
	var owner PublicProfile
	err := r.db.QueryRow(`SELECT u.id,COALESCE(NULLIF(u.nickname,''),u.username),u.avatar_url,u.signature
FROM site_identity s JOIN users u ON u.id=s.creator_id WHERE s.id=1`).
		Scan(&owner.ID, &owner.Name, &owner.AvatarURL, &owner.Signature)
	if errors.Is(err, sql.ErrNoRows) {
		return PublicProfile{}, nil
	}
	return owner, err
}

func (r *Repository) RecentPublicComments() ([]PublicComment, error) {
	rows, err := r.db.Query(`SELECT c.id,c.author_name,c.content,c.created_at,p.slug,p.kind
FROM comments c JOIN posts p ON p.id=c.post_id
WHERE c.status='approved' AND p.kind IN ('post','page') AND p.status='published' AND p.published_at<=?
ORDER BY c.created_at DESC,c.id DESC LIMIT 5`, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	comments := []PublicComment{}
	for rows.Next() {
		var comment PublicComment
		if err := rows.Scan(&comment.ID, &comment.AuthorName, &comment.Content, &comment.CreatedAt, &comment.PostSlug, &comment.PostKind); err != nil {
			return nil, err
		}
		comments = append(comments, comment)
	}
	return comments, rows.Err()
}

func (r *Repository) SearchPublished(query string, limit, offset int) ([]Post, int, error) {
	// A fixed escape character keeps user-entered LIKE wildcards literal on all three databases.
	pattern := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(query) + "%"
	where := `p.kind='post' AND p.status='published' AND p.published_at<=?
AND (LOWER(p.title) LIKE LOWER(?) ESCAPE '!' OR LOWER(p.excerpt) LIKE LOWER(?) ESCAPE '!' OR LOWER(p.content) LIKE LOWER(?) ESCAPE '!')`
	args := []any{time.Now().Unix(), pattern, pattern, pattern}
	var total int
	if err := r.db.QueryRow("SELECT COUNT(*) FROM posts p WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 || offset >= total {
		return []Post{}, total, nil
	}
	rows, err := r.db.Query(`SELECT `+postColumns+` FROM posts p
LEFT JOIN users u ON u.id=p.author_id LEFT JOIN categories c ON c.id=p.category_id
WHERE `+where+` ORDER BY p.is_pinned DESC,p.published_at DESC,p.id DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	posts, err := r.collectPosts(rows)
	return posts, total, err
}

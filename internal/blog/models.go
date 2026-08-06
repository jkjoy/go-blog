package blog

import "time"

type SiteSettings struct {
	Name            string
	Tagline         string
	Description     string
	Keywords        string
	Footer          string
	Beian           string
	FaviconURL      string
	PostsPerPage    int
	Comments        bool
	RequireApproval bool
	PrettyURL       bool
}

type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Nickname     string
	Email        string
	AvatarURL    string
	WebsiteURL   string
	Signature    string
	CreatedAt    int64
}

func (u User) DisplayName() string {
	if u.Nickname != "" {
		return u.Nickname
	}
	return u.Username
}

type Post struct {
	ID            int64
	AuthorID      int64
	CategoryID    int64
	Slug          string
	Title         string
	Excerpt       string
	Content       string
	Kind          string
	TagsJSON      string
	Tags          []Tag
	Views         int64
	Status        string
	PublishedAt   int64
	CreatedAt     int64
	UpdatedAt     int64
	Pinned        bool
	AllowComments bool
	AuthorName    string
	CategoryName  string
	CategorySlug  string
	Rendered      string
	CoverURL      string
}

func (p Post) PublishedTime() time.Time { return time.Unix(p.PublishedAt, 0) }

type Category struct {
	ID          int64
	Name        string
	Slug        string
	Description string
	SortOrder   int
	Count       int
}

type Tag struct {
	Label string
	Slug  string
	Count int
}

type Link struct {
	ID          int64
	Name        string
	URL         string
	IconURL     string
	Description string
	SortOrder   int
}

type Comment struct {
	ID          int64
	PostID      int64
	UserID      int64
	ParentID    int64
	ReplyToName string
	AuthorName  string
	AuthorEmail string
	AuthorURL   string
	Content     string
	Status      string
	IsRead      bool
	CreatedAt   int64
	Children    []Comment
}

type AdminComment struct {
	Comment
	PostTitle string
	PostSlug  string
}

func (c Comment) CreatedTime() time.Time { return time.Unix(c.CreatedAt, 0) }

type DashboardStats struct {
	Posts           int
	Pages           int
	Comments        int
	PendingComments int
	Views           int64
}

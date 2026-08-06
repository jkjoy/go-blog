package blog

const sqliteSchema = `
CREATE TABLE settings(name TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '');
CREATE TABLE ai_settings(name TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '');
CREATE TABLE mail_settings(name TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '');
CREATE TABLE s3_settings(name TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '');
CREATE TABLE users(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  nickname TEXT NOT NULL DEFAULT '', email TEXT NOT NULL DEFAULT '',
  avatar_url TEXT NOT NULL DEFAULT '', website_url TEXT NOT NULL DEFAULT '',
  signature TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL,
  qq_url TEXT NOT NULL DEFAULT '', wechat_url TEXT NOT NULL DEFAULT '',
  weibo_url TEXT NOT NULL DEFAULT '', x_url TEXT NOT NULL DEFAULT '',
  telegram_url TEXT NOT NULL DEFAULT '', mastodon_url TEXT NOT NULL DEFAULT '',
  bilibili_url TEXT NOT NULL DEFAULT '', instagram_url TEXT NOT NULL DEFAULT '',
  tiktok_url TEXT NOT NULL DEFAULT ''
);
CREATE TABLE password_resets(
  id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER NOT NULL,
  token_hash TEXT NOT NULL UNIQUE, expires_at INTEGER NOT NULL,
  used_at INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL,
  FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE TABLE categories(
  id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, slug TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '', sort_order INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE posts(
  id INTEGER PRIMARY KEY AUTOINCREMENT, author_id INTEGER, category_id INTEGER,
  slug TEXT NOT NULL UNIQUE, title TEXT NOT NULL, excerpt TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL, kind TEXT NOT NULL DEFAULT 'post', tags TEXT NOT NULL DEFAULT '[]',
  views INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT 'draft',
  published_at INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  is_pinned INTEGER NOT NULL DEFAULT 0, allow_comments INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE links(
  id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, url TEXT NOT NULL,
  icon_url TEXT NOT NULL DEFAULT '', description TEXT NOT NULL DEFAULT '',
  sort_order INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE tag_meta(label TEXT NOT NULL UNIQUE, slug TEXT NOT NULL UNIQUE, updated_at INTEGER NOT NULL);
CREATE TABLE comments(
  id INTEGER PRIMARY KEY AUTOINCREMENT, post_id INTEGER NOT NULL, user_id INTEGER,
  parent_id INTEGER REFERENCES comments(id) ON DELETE SET NULL,
  reply_to_name TEXT NOT NULL DEFAULT '', author_name TEXT NOT NULL, author_email TEXT NOT NULL,
  author_url TEXT NOT NULL DEFAULT '', content TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending',
  is_read INTEGER NOT NULL DEFAULT 0, ip_hash TEXT NOT NULL DEFAULT '', ip_address TEXT NOT NULL DEFAULT '',
  user_agent TEXT NOT NULL DEFAULT '', reply_notified_at INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  FOREIGN KEY(post_id) REFERENCES posts(id) ON DELETE CASCADE,
  FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE SET NULL
);
CREATE TABLE post_views(
  post_id INTEGER NOT NULL, ip_hash TEXT NOT NULL, created_at INTEGER NOT NULL,
  PRIMARY KEY(post_id, ip_hash), FOREIGN KEY(post_id) REFERENCES posts(id) ON DELETE CASCADE
) WITHOUT ROWID;
CREATE INDEX idx_posts_public_pinned ON posts(kind, status, is_pinned DESC, published_at DESC, id DESC);
CREATE INDEX idx_posts_kind_updated ON posts(kind, updated_at DESC, id DESC);
CREATE INDEX idx_posts_category ON posts(category_id, kind, status, published_at DESC);
CREATE INDEX idx_categories_sort ON categories(sort_order ASC, id DESC);
CREATE INDEX idx_links_sort ON links(sort_order ASC, id DESC);
CREATE INDEX idx_comments_post_public ON comments(post_id, status, created_at, id);
CREATE INDEX idx_comments_moderation ON comments(status, created_at DESC, id DESC);
CREATE INDEX idx_comments_parent ON comments(parent_id, created_at, id);
`

var requiredTables = []string{
	"settings", "users", "posts", "categories", "links", "tag_meta", "comments", "post_views",
}

const postgresSchema = `
CREATE TABLE IF NOT EXISTS settings(name TEXT PRIMARY KEY,value TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS ai_settings(name TEXT PRIMARY KEY,value TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS mail_settings(name TEXT PRIMARY KEY,value TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS s3_settings(name TEXT PRIMARY KEY,value TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS users(id BIGSERIAL PRIMARY KEY,username TEXT NOT NULL UNIQUE,password_hash TEXT NOT NULL,nickname TEXT NOT NULL DEFAULT '',email TEXT NOT NULL DEFAULT '',avatar_url TEXT NOT NULL DEFAULT '',website_url TEXT NOT NULL DEFAULT '',signature TEXT NOT NULL DEFAULT '',created_at BIGINT NOT NULL,qq_url TEXT NOT NULL DEFAULT '',wechat_url TEXT NOT NULL DEFAULT '',weibo_url TEXT NOT NULL DEFAULT '',x_url TEXT NOT NULL DEFAULT '',telegram_url TEXT NOT NULL DEFAULT '',mastodon_url TEXT NOT NULL DEFAULT '',bilibili_url TEXT NOT NULL DEFAULT '',instagram_url TEXT NOT NULL DEFAULT '',tiktok_url TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS password_resets(id BIGSERIAL PRIMARY KEY,user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,token_hash TEXT NOT NULL UNIQUE,expires_at BIGINT NOT NULL,used_at BIGINT NOT NULL DEFAULT 0,created_at BIGINT NOT NULL);
CREATE TABLE IF NOT EXISTS categories(id BIGSERIAL PRIMARY KEY,name TEXT NOT NULL,slug TEXT NOT NULL UNIQUE,description TEXT NOT NULL DEFAULT '',sort_order INTEGER NOT NULL DEFAULT 0,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL);
CREATE TABLE IF NOT EXISTS posts(id BIGSERIAL PRIMARY KEY,author_id BIGINT,category_id BIGINT,slug TEXT NOT NULL UNIQUE,title TEXT NOT NULL,excerpt TEXT NOT NULL DEFAULT '',content TEXT NOT NULL,kind TEXT NOT NULL DEFAULT 'post',tags TEXT NOT NULL DEFAULT '[]',views BIGINT NOT NULL DEFAULT 0,status TEXT NOT NULL DEFAULT 'draft',published_at BIGINT NOT NULL DEFAULT 0,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL,is_pinned INTEGER NOT NULL DEFAULT 0,allow_comments INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS links(id BIGSERIAL PRIMARY KEY,name TEXT NOT NULL,url TEXT NOT NULL,icon_url TEXT NOT NULL DEFAULT '',description TEXT NOT NULL DEFAULT '',sort_order INTEGER NOT NULL DEFAULT 0,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL);
CREATE TABLE IF NOT EXISTS tag_meta(label TEXT NOT NULL UNIQUE,slug TEXT NOT NULL UNIQUE,updated_at BIGINT NOT NULL);
CREATE TABLE IF NOT EXISTS comments(id BIGSERIAL PRIMARY KEY,post_id BIGINT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,parent_id BIGINT REFERENCES comments(id) ON DELETE SET NULL,reply_to_name TEXT NOT NULL DEFAULT '',author_name TEXT NOT NULL,author_email TEXT NOT NULL,author_url TEXT NOT NULL DEFAULT '',content TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'pending',is_read INTEGER NOT NULL DEFAULT 0,ip_hash TEXT NOT NULL DEFAULT '',ip_address TEXT NOT NULL DEFAULT '',user_agent TEXT NOT NULL DEFAULT '',reply_notified_at BIGINT NOT NULL DEFAULT 0,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL);
CREATE TABLE IF NOT EXISTS post_views(post_id BIGINT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,ip_hash TEXT NOT NULL,created_at BIGINT NOT NULL,PRIMARY KEY(post_id,ip_hash));
CREATE INDEX IF NOT EXISTS idx_posts_public_pinned ON posts(kind,status,is_pinned DESC,published_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS idx_posts_category ON posts(category_id,kind,status,published_at DESC);
CREATE INDEX IF NOT EXISTS idx_comments_post_public ON comments(post_id,status,created_at,id);
`

const mysqlSchema = `
CREATE TABLE IF NOT EXISTS settings(name VARCHAR(191) PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS ai_settings(name VARCHAR(191) PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS mail_settings(name VARCHAR(191) PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS s3_settings(name VARCHAR(191) PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS users(id BIGINT AUTO_INCREMENT PRIMARY KEY,username VARCHAR(191) NOT NULL UNIQUE,password_hash VARCHAR(255) NOT NULL,nickname VARCHAR(255) NOT NULL DEFAULT '',email VARCHAR(255) NOT NULL DEFAULT '',avatar_url TEXT NOT NULL,website_url TEXT NOT NULL,signature TEXT NOT NULL,created_at BIGINT NOT NULL,qq_url TEXT NOT NULL,wechat_url TEXT NOT NULL,weibo_url TEXT NOT NULL,x_url TEXT NOT NULL,telegram_url TEXT NOT NULL,mastodon_url TEXT NOT NULL,bilibili_url TEXT NOT NULL,instagram_url TEXT NOT NULL,tiktok_url TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS password_resets(id BIGINT AUTO_INCREMENT PRIMARY KEY,user_id BIGINT NOT NULL,token_hash VARCHAR(191) NOT NULL UNIQUE,expires_at BIGINT NOT NULL,used_at BIGINT NOT NULL DEFAULT 0,created_at BIGINT NOT NULL,FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS categories(id BIGINT AUTO_INCREMENT PRIMARY KEY,name VARCHAR(255) NOT NULL,slug VARCHAR(191) NOT NULL UNIQUE,description TEXT NOT NULL,sort_order INT NOT NULL DEFAULT 0,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL);
CREATE TABLE IF NOT EXISTS posts(id BIGINT AUTO_INCREMENT PRIMARY KEY,author_id BIGINT NULL,category_id BIGINT NULL,slug VARCHAR(191) NOT NULL UNIQUE,title VARCHAR(500) NOT NULL,excerpt TEXT NOT NULL,content LONGTEXT NOT NULL,kind VARCHAR(20) NOT NULL DEFAULT 'post',tags TEXT NOT NULL,views BIGINT NOT NULL DEFAULT 0,status VARCHAR(20) NOT NULL DEFAULT 'draft',published_at BIGINT NOT NULL DEFAULT 0,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL,is_pinned TINYINT NOT NULL DEFAULT 0,allow_comments TINYINT NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS links(id BIGINT AUTO_INCREMENT PRIMARY KEY,name VARCHAR(255) NOT NULL,url TEXT NOT NULL,icon_url TEXT NOT NULL,description TEXT NOT NULL,sort_order INT NOT NULL DEFAULT 0,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL);
CREATE TABLE IF NOT EXISTS tag_meta(label VARCHAR(191) NOT NULL UNIQUE,slug VARCHAR(191) NOT NULL UNIQUE,updated_at BIGINT NOT NULL);
CREATE TABLE IF NOT EXISTS comments(id BIGINT AUTO_INCREMENT PRIMARY KEY,post_id BIGINT NOT NULL,user_id BIGINT NULL,parent_id BIGINT NULL,reply_to_name VARCHAR(255) NOT NULL DEFAULT '',author_name VARCHAR(255) NOT NULL,author_email VARCHAR(255) NOT NULL,author_url TEXT NOT NULL,content TEXT NOT NULL,status VARCHAR(20) NOT NULL DEFAULT 'pending',is_read TINYINT NOT NULL DEFAULT 0,ip_hash VARCHAR(255) NOT NULL DEFAULT '',ip_address VARCHAR(64) NOT NULL DEFAULT '',user_agent TEXT NOT NULL,reply_notified_at BIGINT NOT NULL DEFAULT 0,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL,FOREIGN KEY(post_id) REFERENCES posts(id) ON DELETE CASCADE,FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE SET NULL,FOREIGN KEY(parent_id) REFERENCES comments(id) ON DELETE SET NULL);
CREATE TABLE IF NOT EXISTS post_views(post_id BIGINT NOT NULL,ip_hash VARCHAR(191) NOT NULL,created_at BIGINT NOT NULL,PRIMARY KEY(post_id,ip_hash),FOREIGN KEY(post_id) REFERENCES posts(id) ON DELETE CASCADE);
CREATE INDEX idx_posts_public_pinned ON posts(kind,status,is_pinned,published_at,id);
CREATE INDEX idx_posts_category ON posts(category_id,kind,status,published_at);
CREATE INDEX idx_comments_post_public ON comments(post_id,status,created_at,id);
`

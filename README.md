# Go Blog

一个可打包为单个可执行文件的轻量博客。前台参考 Nebula 主题，支持深浅主题、星空背景、文章归档、标签、分类、友链、评论和响应式布局。

## 当前功能

- 使用现有 SQLite 表结构，无需导入导出
- 首页、文章、独立页面、归档、标签、分类和友链
- Goldmark Markdown 渲染与 HTML 白名单清理
- 评论、嵌套回复、审核、SMTP 邮件通知和浏览计数
- RSS 2.0 和 Sitemap
- bcrypt 密码验证
- 签名登录会话、CSRF 校验和安全响应头
- 文章、页面、评论、分类、友链和站点设置管理
- 本地或 S3 兼容对象存储附件上传，并自动插入 Markdown
- OpenAI 兼容接口自动生成摘要、翻译 Slug 和按需润色文章
- SQLite、PostgreSQL 和 MySQL/MariaDB 数据库驱动
- 模板、CSS 和 JavaScript 通过 `go:embed` 嵌入程序

## 构建

需要 Go 1.25 或更高版本：

```bash
go test ./...
go build -trimpath -ldflags "-s -w -X main.version=0.0.1" -o go-blog ./cmd/blog
```

构建产物只需要一个 `go-blog` 文件，不需要额外运行时、Node.js、系统 SQLite 或 CGO。

## Docker Compose 部署

先设置至少 32 字节的会话密钥，然后构建并启动：

```bash
export BLOG_SESSION_SECRET="$(openssl rand -hex 32)"
docker compose up -d --build
```

默认访问地址为 `http://127.0.0.1:8080`。首次启动生成的管理员密码只会出现在日志中：

```bash
docker compose logs blog
```

数据库和上传文件分别保存在 `blog-data`、`blog-uploads` 命名卷中。升级和停止服务：

```bash
docker compose up -d --build
docker compose down
```

`docker compose down` 不会删除数据卷；不要使用 `docker compose down -v`，除非确定要删除全部博客数据。端口、版本和 HTTPS Cookie 可通过环境变量调整：

```bash
BLOG_PORT=9000 BLOG_VERSION=0.0.1 BLOG_SECURE_COOKIES=true docker compose up -d --build
```

部署现有 SQLite 数据库前，先停止原博客并完成在线备份，再将备份文件放入 Compose 的 `blog-data` 卷中，文件名使用 `blog.sqlite`。同一数据库文件不能同时由两个博客实例写入。

连接外部 PostgreSQL 或 MySQL 时设置 `BLOG_DATABASE_DRIVER` 和 `BLOG_DATABASE_DSN`。空数据库首次建表还需设置 `BLOG_SCHEMA_MODE=init`；已有表结构保持默认的 `validate`。

## GitHub 镜像发布

工作流 `.github/workflows/container.yml` 会将镜像发布到 `ghcr.io/jkjoy/go-blog`：

- 推送到 `main`：发布 `main`、`sha-*` 和 `latest` 标签
- 推送 `v1.2.3` 格式的稳定版 Git 标签：发布 `1.2.3`、`1.2`、`sha-*` 和 `latest`，并创建 GitHub Release
- Pull Request：构建 AMD64/ARM64 镜像进行验证，但不推送
- GitHub Actions 页面支持手动触发

工作流使用仓库自带的 `GITHUB_TOKEN`，仓库权限需要允许 Actions 写入 Packages。发布后可以直接使用远程镜像部署：

```bash
export BLOG_SESSION_SECRET="$(openssl rand -hex 32)"
docker compose pull
docker compose up -d --no-build
```

指定版本时设置 `BLOG_VERSION`，例如 `BLOG_VERSION=1.2.3 docker compose up -d --no-build`。私有仓库首次拉取镜像前，需要先使用具有 `read:packages` 权限的令牌登录 `ghcr.io`。

## 使用 SQLite 启动

使用新数据库：

```bash
./go-blog
```

第一次启动会创建 `data/blog.sqlite`、`uploads/` 和管理员账号。随机管理员密码只打印一次，应立即保存。

使用现有 SQLite 数据库：

```bash
BLOG_SESSION_SECRET='至少32位的随机字符串' \
./go-blog --database /path/to/blog.sqlite
```

程序不会在启动时对现有数据库执行自动迁移。正式切换前应备份数据库，并确保只有一个程序写入同一个 SQLite 文件。

## PostgreSQL 和 MySQL

已有兼容表结构时使用默认的 `validate` 模式：

```bash
BLOG_DATABASE_DRIVER=postgres \
BLOG_DATABASE_DSN='postgres://user:password@127.0.0.1/blog?sslmode=disable' \
./go-blog
```

初始化空数据库时显式开启 `init`：

```bash
BLOG_DATABASE_DRIVER=mysql \
BLOG_DATABASE_DSN='user:password@tcp(127.0.0.1:3306)/blog?charset=utf8mb4&parseTime=true' \
BLOG_SCHEMA_MODE=init \
./go-blog
```

`init` 只用于创建当前版本的空库，不替代后续版本化迁移。

## 配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `BLOG_LISTEN` | `127.0.0.1:8080` | HTTP 监听地址 |
| `BLOG_DATABASE_DRIVER` | `sqlite` | `sqlite`、`postgres` 或 `mysql` |
| `BLOG_DATABASE_DSN` | `data/blog.sqlite` | 数据库文件或连接串 |
| `BLOG_SCHEMA_MODE` | `validate` | `validate` 或 `init` |
| `BLOG_UPLOAD_DIR` | `uploads` | 上传文件目录 |
| `BLOG_SESSION_SECRET` | 空 | 至少 32 字节；生产环境必须设置 |
| `BLOG_TRUSTED_PROXY` | 空 | 可信反向代理 IP/CIDR，多个值用逗号分隔 |
| `BLOG_SECURE_COOKIES` | `false` | HTTPS 部署时设置为 `true` |
| `BLOG_LOG_LEVEL` | `info` | `debug`、`info`、`warn`、`error` |

同名命令行参数包括 `--listen`、`--database-driver`、`--database`、`--schema-mode` 和 `--uploads`。

反向代理需要转发 `Host`、`X-Forwarded-For` 和 `X-Forwarded-Proto`，并将 `/` 代理到程序监听地址。将代理服务器的地址或网段写入 `BLOG_TRUSTED_PROXY`，例如 `127.0.0.1,172.18.0.0/16`；不要填写不受控制的公网网段。生产环境建议只通过 HTTPS 暴露服务，并设置 `BLOG_SECURE_COOKIES=true`。

`users` 表中 ID 最小的第一个用户是站点创建者，可访问完整管理后台；其余用户属于订阅者，只能阅读和评论。登录与评论接口带有进程内频率限制，多实例部署时应在反向代理层再配置共享限流。

评论邮件通知、S3 兼容上传和 AI 辅助在后台“设置”页面配置。SMTP 支持无加密、STARTTLS 和隐式 TLS/SSL；S3 支持自定义 Endpoint、Path-style、公开 URL、路径前缀和本地副本保留。AI 辅助支持 OpenAI 兼容的 Chat Completions 接口，可配置模型以及摘要、Slug、润色提示词。已保存的 SMTP 密码、Access Key、Secret Key 和 AI API Key 不会回显到管理页面，密钥输入框留空会保留原值。

## 数据目录

运行数据不能嵌入可执行文件，应在升级二进制时保留：

```text
data/blog.sqlite
uploads/
```

SQLite 备份时应停止写入后使用 SQLite 的在线备份命令或同时保存 `-wal` 文件，不要在写入期间只复制主数据库文件。

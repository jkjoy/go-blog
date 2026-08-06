package blog

import (
	"bytes"
	"html/template"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

type Markdown struct {
	engine goldmark.Markdown
	policy *bluemonday.Policy
}

func NewMarkdown() *Markdown {
	policy := bluemonday.UGCPolicy()
	policy.AllowAttrs("class").OnElements("table", "thead", "tbody", "tr", "th", "td", "pre", "code")
	policy.AllowAttrs("id").Matching(regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)).OnElements("h1", "h2", "h3", "h4", "h5", "h6")
	return &Markdown{
		engine: goldmark.New(
			goldmark.WithExtensions(extension.GFM, extension.Footnote, extension.Typographer),
			goldmark.WithParserOptions(parser.WithAutoHeadingID()),
			goldmark.WithRendererOptions(html.WithUnsafe()),
		),
		policy: policy,
	}
}

func (m *Markdown) Render(source string) template.HTML {
	var buffer bytes.Buffer
	if err := m.engine.Convert([]byte(source), &buffer); err != nil {
		return template.HTML(template.HTMLEscapeString(source))
	}
	return template.HTML(m.policy.SanitizeBytes(buffer.Bytes()))
}

var (
	markdownImage     = regexp.MustCompile(`!\[[^\]]*\]\((https?://[^\s)]+|/[^\s)]+)(?:\s+["'][^"']*["'])?\)`)
	htmlImage         = regexp.MustCompile(`(?i)<img[^>]+src=["']([^"']+)["']`)
	fencedCode        = regexp.MustCompile("(?s)```.*?```")
	markdownImageText = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	markdownLink      = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	linePrefix        = regexp.MustCompile(`(?m)^\s*(?:#{1,6}|[-+*>]|\d+[.)])\s*`)
	inlinePrefix      = regexp.MustCompile(`\s+(?:#{1,6}|[-+*>])\s+`)
	htmlTag           = regexp.MustCompile(`(?s)<[^>]*>`)
	markdownMarks     = regexp.MustCompile("[*_~`]+")
	spaceRun          = regexp.MustCompile(`\s+`)
)

func PostCover(content string) string {
	for _, pattern := range []*regexp.Regexp{markdownImage, htmlImage} {
		if match := pattern.FindStringSubmatch(content); len(match) > 1 {
			return match[1]
		}
	}
	return ""
}

func DeriveExcerpt(post Post, max int) string {
	text := strings.TrimSpace(post.Excerpt)
	if text == "" {
		text = post.Content
	}
	text = fencedCode.ReplaceAllString(text, " ")
	text = markdownImageText.ReplaceAllString(text, " ")
	text = markdownLink.ReplaceAllString(text, "$1")
	text = linePrefix.ReplaceAllString(text, "")
	text = inlinePrefix.ReplaceAllString(text, " ")
	text = htmlTag.ReplaceAllString(text, " ")
	text = markdownMarks.ReplaceAllString(text, "")
	text = strings.TrimSpace(spaceRun.ReplaceAllString(text, " "))
	runes := []rune(text)
	if len(runes) > max {
		return string(runes[:max]) + "..."
	}
	return text
}

package blog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	defaultAISummaryPrompt = "根据文章内容生成不超过100个汉字的中文摘要。只输出摘要正文，不要标题、引号、解释或 Markdown 标记。"
	defaultAISlugPrompt    = "Translate the title into a concise English URL slug. Output lowercase ASCII words separated only by hyphens. Output the slug only, without quotes or explanation."
	defaultAIPolishPrompt  = "你是专业中文编辑。严格执行用户要求，保留有效 Markdown 结构。只输出处理后的完整正文，不要解释处理过程。"
)

type aiCompleter func(context.Context, *http.Client, AISettings, string, string, int) (string, error)

type aiCompletionRequest struct {
	Model       string      `json:"model"`
	Messages    []aiMessage `json:"messages"`
	Temperature float64     `json:"temperature"`
	MaxTokens   int         `json:"max_tokens"`
}

type aiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type aiCompletionResponse struct {
	Choices []struct {
		Message aiMessage `json:"message"`
	} `json:"choices"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

func requestAICompletion(ctx context.Context, client *http.Client, settings AISettings, systemPrompt, userPrompt string, maxTokens int) (string, error) {
	endpoint, err := aiCompletionURL(settings.APIURL)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(aiCompletionRequest{
		Model: settings.Model, Temperature: 0.2, MaxTokens: maxTokens,
		Messages: []aiMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: userPrompt}},
	})
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+settings.APIKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil {
		return "", err
	}
	if len(body) > 2<<20 {
		return "", fmt.Errorf("AI 响应超过 2MB")
	}
	var completion aiCompletionResponse
	if err := json.Unmarshal(body, &completion); err != nil {
		return "", fmt.Errorf("AI 服务返回了无效响应")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(completion.Error.Message)
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		return "", fmt.Errorf("AI 服务返回 %d：%s", response.StatusCode, truncateRunes(message, 300))
	}
	if len(completion.Choices) == 0 || strings.TrimSpace(completion.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("AI 服务没有返回内容")
	}
	return strings.TrimSpace(completion.Choices[0].Message.Content), nil
}

func aiCompletionURL(base string) (string, error) {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(base))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("AI API URL 不正确")
	}
	if !strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), "/chat/completions") {
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/chat/completions"
	}
	return parsed.String(), nil
}

func normalizeAISlug(value string) string {
	value = strings.ToLower(strings.Trim(strings.TrimSpace(value), "`\"'"))
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	value = strings.Join(parts, "-")
	if len(value) > 180 {
		value = strings.TrimRight(value[:180], "-")
	}
	return value
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

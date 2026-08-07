package blog

import (
	"fmt"
	"net/mail"
	"net/url"
	"strings"
)

func validateMailSettings(settings MailSettings) error {
	if !settings.Enabled {
		return nil
	}
	if settings.Host == "" {
		return fmt.Errorf("启用邮件通知时必须填写 SMTP 主机")
	}
	if settings.Port < 1 || settings.Port > 65535 {
		return fmt.Errorf("SMTP 端口应为 1 至 65535")
	}
	switch settings.Encryption {
	case "none", "starttls", "ssl", "tls":
	default:
		return fmt.Errorf("请选择有效的 SMTP 加密方式")
	}
	if _, err := mail.ParseAddress(settings.FromEmail); err != nil {
		return fmt.Errorf("发件邮箱格式不正确")
	}
	if _, err := mail.ParseAddress(settings.NotifyEmail); err != nil {
		return fmt.Errorf("通知邮箱格式不正确")
	}
	if settings.Username != "" && settings.Password == "" {
		return fmt.Errorf("填写 SMTP 用户名后必须配置密码")
	}
	return nil
}

func validateS3Settings(settings S3Settings) error {
	if !settings.Enabled {
		return nil
	}
	if err := validateServiceURL(settings.Endpoint, "S3 Endpoint"); err != nil {
		return err
	}
	if err := validateServiceURL(settings.PublicURL, "S3 公开 URL"); err != nil {
		return err
	}
	if settings.Region == "" || settings.Bucket == "" {
		return fmt.Errorf("启用 S3 时必须填写区域和 Bucket")
	}
	if settings.AccessKey == "" || settings.SecretKey == "" {
		return fmt.Errorf("启用 S3 时必须配置 Access Key 和 Secret Key")
	}
	if _, err := normalizeS3Prefix(settings.PathPrefix); err != nil {
		return err
	}
	return nil
}

func validateAISettings(settings AISettings) error {
	if settings.APIURL == "" && settings.APIKey == "" && settings.Model == "" {
		return nil
	}
	if _, err := aiCompletionURL(settings.APIURL); err != nil {
		return err
	}
	if settings.APIKey == "" || settings.Model == "" {
		return fmt.Errorf("启用 AI 时必须配置 API Key 和模型")
	}
	if len([]rune(settings.Model)) > 200 {
		return fmt.Errorf("AI 模型名称不能超过 200 个字符")
	}
	for name, value := range map[string]string{
		"摘要提示词": settings.SummaryPrompt, "Slug 提示词": settings.SlugPrompt, "润色提示词": settings.PolishPrompt,
	} {
		length := len([]rune(strings.TrimSpace(value)))
		if length < 1 || length > 5000 {
			return fmt.Errorf("%s长度应为 1 至 5000 个字符", name)
		}
	}
	return nil
}

func validateServiceURL(value, label string) error {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%s 必须是有效的 HTTP 或 HTTPS 地址", label)
	}
	return nil
}

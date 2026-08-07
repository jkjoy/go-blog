package blog

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

type mailSender func(context.Context, MailSettings, string, string, string) error

func (a *App) queueCommentNotifications(commentID int64, publicURL, adminURL string, notifyAdmin, notifyReply bool) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := a.sendCommentNotifications(ctx, commentID, publicURL, adminURL, notifyAdmin, notifyReply); err != nil {
			a.logger.Warn("send comment notification failed", "comment_id", commentID, "error", err)
		}
	}()
}

func (a *App) sendCommentNotifications(ctx context.Context, commentID int64, publicURL, adminURL string, notifyAdmin, notifyReply bool) error {
	settings, err := a.repo.Settings()
	if err != nil {
		return err
	}
	if !settings.NotifyComments {
		return nil
	}
	mailSettings, err := a.repo.MailSettings()
	if err != nil {
		return err
	}
	if !mailSettings.Enabled {
		return nil
	}
	if mailSettings.FromName == "" {
		mailSettings.FromName = settings.Name
	}
	comment, err := a.repo.CommentNotification(commentID)
	if err != nil {
		return err
	}

	var sendErrors []error
	if notifyAdmin && mailSettings.NotifyEmail != "" {
		subject := fmt.Sprintf("[%s] 新评论：%s", settings.Name, comment.PostTitle)
		body := fmt.Sprintf("%s <%s> 提交了评论：\n\n%s\n\n文章：%s\n查看评论：%s\n",
			comment.AuthorName, comment.AuthorEmail, notificationExcerpt(comment.Content), comment.PostTitle, adminURL)
		if err := a.sendMail(ctx, mailSettings, mailSettings.NotifyEmail, subject, body); err != nil {
			sendErrors = append(sendErrors, fmt.Errorf("notify administrator: %w", err))
		}
	}
	if notifyReply && comment.Status == "approved" && comment.ParentID > 0 && comment.ReplyNotifiedAt == 0 &&
		comment.ParentEmail != "" && !strings.EqualFold(comment.ParentEmail, comment.AuthorEmail) {
		subject := fmt.Sprintf("[%s] %s 回复了你的评论", settings.Name, comment.AuthorName)
		body := fmt.Sprintf("%s，你好：\n\n%s 在《%s》中回复了你：\n\n%s\n\n查看回复：%s#comment-%d\n",
			comment.ParentName, comment.AuthorName, comment.PostTitle, notificationExcerpt(comment.Content), publicURL, comment.ID)
		if err := a.sendMail(ctx, mailSettings, comment.ParentEmail, subject, body); err != nil {
			sendErrors = append(sendErrors, fmt.Errorf("notify parent commenter: %w", err))
		} else if err := a.repo.MarkReplyNotified(comment.ID); err != nil {
			sendErrors = append(sendErrors, fmt.Errorf("mark reply notified: %w", err))
		}
	}
	return errors.Join(sendErrors...)
}

func notificationExcerpt(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > 1000 {
		return string(runes[:1000]) + "..."
	}
	return string(runes)
}

func sendSMTPMail(ctx context.Context, settings MailSettings, recipient, subject, body string) error {
	to, err := mail.ParseAddress(recipient)
	if err != nil {
		return fmt.Errorf("invalid recipient: %w", err)
	}
	from, err := mail.ParseAddress(settings.FromEmail)
	if err != nil {
		return fmt.Errorf("invalid sender: %w", err)
	}
	from.Name = settings.FromName
	address := net.JoinHostPort(settings.Host, strconv.Itoa(settings.Port))
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: settings.Host}

	var connection net.Conn
	if settings.Encryption == "ssl" || settings.Encryption == "tls" {
		connection, err = tls.DialWithDialer(dialer, "tcp", address, tlsConfig)
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return err
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}

	client, err := smtp.NewClient(connection, settings.Host)
	if err != nil {
		return err
	}
	defer client.Close()
	if settings.Encryption == "starttls" {
		if err := client.StartTLS(tlsConfig); err != nil {
			return err
		}
	}
	if settings.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", settings.Username, settings.Password, settings.Host)); err != nil {
			return err
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return err
	}
	if err := client.Rcpt(to.Address); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	message := strings.Join([]string{
		"From: " + from.String(),
		"To: " + to.String(),
		"Subject: " + mime.QEncoding.Encode("UTF-8", strings.ReplaceAll(strings.ReplaceAll(subject, "\r", ""), "\n", "")),
		"Date: " + time.Now().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n"),
	}, "\r\n")
	if _, err := writer.Write([]byte(message)); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

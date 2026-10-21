package blog

import (
	"context"
	"errors"
	"time"
)

// StartNotifications starts one bounded worker. Call after configuring any
// injected services; tasks already in the database are recovered immediately.
func (a *App) StartNotifications() {
	a.notificationMu.Lock()
	defer a.notificationMu.Unlock()
	if a.notificationClosed || a.notificationCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.notificationCancel = cancel
	a.notificationDone = make(chan struct{})
	go func() {
		defer close(a.notificationDone)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			if err := a.processNotificationJobs(ctx); err != nil && ctx.Err() == nil {
				a.logger.Warn("process notification queue failed", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-a.notificationWake:
			}
		}
	}()
}

func (a *App) wakeNotifications() {
	select {
	case a.notificationWake <- struct{}{}:
	default:
	}
}

func (a *App) processNotificationJobs(ctx context.Context) error {
	settings, err := a.repo.Settings()
	if err != nil {
		return err
	}
	mail, err := a.repo.MailSettings()
	if err != nil {
		return err
	}
	// Pausing notifications preserves pending tasks and their retry budget.
	if !settings.NotifyComments || !mail.Enabled {
		return nil
	}
	jobs, err := a.repo.dueNotificationJobs(ctx)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if job.Kind == "reply" {
			comment, err := a.repo.CommentNotification(job.CommentID)
			if IsNotFound(err) {
				continue
			}
			if err != nil {
				return err
			}
			// Recover a crashed sender only after its reply lease also expires.
			if comment.ReplyNotifiedAt < 0 && comment.ReplyNotifiedAt <= -time.Now().Add(-5*time.Minute).Unix() {
				continue
			}
		}
		claimed, err := a.repo.claimNotificationJob(ctx, &job)
		if err != nil {
			return err
		}
		if !claimed {
			continue
		}
		sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		sendErr := a.sendCommentNotifications(sendCtx, job.CommentID, job.PublicURL, job.AdminURL, job.Kind == "admin", job.Kind == "reply")
		cancel()
		if sendErr != nil && !errors.Is(sendErr, errNotificationsPaused) {
			a.logger.Warn("notification delivery failed", "comment_id", job.CommentID, "kind", job.Kind, "attempt", job.Attempts, "error", sendErr)
		}
		if err := a.repo.finishNotificationJob(job, sendErr); err != nil {
			return err
		}
	}
	return nil
}

package blog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const maxNotificationAttempts = 8

type NotificationJob struct {
	CommentID     int64
	Kind          string
	PublicURL     string
	AdminURL      string
	Attempts      int
	NextAttemptAt int64
	ClaimToken    string
	ClaimedUntil  int64
	CompletedAt   int64
	FailedAt      int64
	LastError     string
	CreatedAt     int64
}

func (j NotificationJob) State() string {
	switch {
	case j.CompletedAt > 0:
		return "已处理"
	case j.FailedAt > 0:
		return "发送失败"
	case j.ClaimedUntil > time.Now().Unix():
		return "发送中"
	case j.Attempts > 0:
		return "等待重试"
	default:
		return "等待发送"
	}
}

func (r *Repository) enqueueNotifications(tx *sql.Tx, commentID int64, publicURL, adminURL string, admin, reply bool) error {
	var enabled int
	err := tx.QueryRow(`SELECT
(SELECT COUNT(*) FROM settings WHERE name='comments_notify' AND value='1') *
(SELECT COUNT(*) FROM mail_settings WHERE name='smtp_enabled' AND value='1')`).Scan(&enabled)
	if err != nil || enabled == 0 {
		return err
	}
	for _, kind := range []string{"admin", "reply"} {
		if kind == "admin" && !admin || kind == "reply" && !reply {
			continue
		}
		query := `INSERT INTO notification_jobs(comment_id,kind,public_url,admin_url,created_at)
SELECT id,?,?,?,? FROM comments WHERE id=?`
		if kind == "reply" {
			query += " AND parent_id IS NOT NULL AND status='approved' AND reply_notified_at<=0"
		}
		if r.db.Dialect == "mysql" {
			query += " ON DUPLICATE KEY UPDATE comment_id=notification_jobs.comment_id"
		} else {
			query += " ON CONFLICT(comment_id,kind) DO NOTHING"
		}
		if _, err := tx.Exec(r.db.Bind(query), kind, publicURL, adminURL, time.Now().Unix(), commentID); err != nil {
			return err
		}
	}
	return nil
}

const notificationColumns = `comment_id,kind,public_url,admin_url,attempts,next_attempt_at,claim_token,claimed_until,completed_at,failed_at,last_error,created_at`

func collectNotificationJobs(rows *sql.Rows) ([]NotificationJob, error) {
	defer rows.Close()
	jobs := []NotificationJob{}
	for rows.Next() {
		var j NotificationJob
		if err := rows.Scan(&j.CommentID, &j.Kind, &j.PublicURL, &j.AdminURL, &j.Attempts, &j.NextAttemptAt, &j.ClaimToken, &j.ClaimedUntil, &j.CompletedAt, &j.FailedAt, &j.LastError, &j.CreatedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (r *Repository) NotificationJobs() ([]NotificationJob, error) {
	rows, err := r.db.Query("SELECT " + notificationColumns + " FROM notification_jobs ORDER BY created_at DESC,comment_id DESC,kind LIMIT 100")
	if err != nil {
		return nil, err
	}
	return collectNotificationJobs(rows)
}

func (r *Repository) dueNotificationJobs(ctx context.Context) ([]NotificationJob, error) {
	now := time.Now().Unix()
	if _, err := r.db.DB.ExecContext(ctx, r.db.Bind(`UPDATE notification_jobs SET failed_at=?,claim_token='',claimed_until=0,
last_error='发送尝试次数已达上限，请检查配置后手动重试' WHERE completed_at=0 AND failed_at=0 AND attempts>=? AND claimed_until<=?`), now, maxNotificationAttempts, now); err != nil {
		return nil, err
	}
	rows, err := r.db.DB.QueryContext(ctx, r.db.Bind("SELECT "+notificationColumns+` FROM notification_jobs
WHERE completed_at=0 AND failed_at=0 AND next_attempt_at<=? AND claimed_until<=?
AND (kind='admin' OR EXISTS(SELECT 1 FROM comments WHERE comments.id=notification_jobs.comment_id AND comments.status='approved'))
ORDER BY next_attempt_at,created_at,comment_id,kind LIMIT 20`), now, now)
	if err != nil {
		return nil, err
	}
	return collectNotificationJobs(rows)
}

func (r *Repository) claimNotificationJob(ctx context.Context, job *NotificationJob) (bool, error) {
	token, err := randomToken(24)
	if err != nil {
		return false, err
	}
	now := time.Now().Unix()
	result, err := r.db.DB.ExecContext(ctx, r.db.Bind(`UPDATE notification_jobs SET claim_token=?,claimed_until=?,attempts=attempts+1
WHERE comment_id=? AND kind=? AND completed_at=0 AND failed_at=0 AND attempts<? AND next_attempt_at<=? AND claimed_until<=?`), token, now+300, job.CommentID, job.Kind, maxNotificationAttempts, now, now)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err == nil && affected == 1 {
		job.ClaimToken = token
		err = r.db.DB.QueryRowContext(ctx, r.db.Bind("SELECT attempts FROM notification_jobs WHERE comment_id=? AND kind=? AND claim_token=?"), job.CommentID, job.Kind, token).Scan(&job.Attempts)
	}
	return affected == 1, err
}

func (r *Repository) finishNotificationJob(job NotificationJob, sendErr error) error {
	now := time.Now().Unix()
	var completed, failed, next int64
	message := ""
	if errors.Is(sendErr, errNotificationsPaused) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := r.db.DB.ExecContext(ctx, r.db.Bind(`UPDATE notification_jobs SET claim_token='',claimed_until=0,attempts=attempts-1,next_attempt_at=? WHERE comment_id=? AND kind=? AND claim_token=?`), now+30, job.CommentID, job.Kind, job.ClaimToken)
		return err
	}
	if sendErr == nil {
		completed = now
	} else {
		// SMTP errors can contain server replies or credentials. Keep only a
		// generic error in the database/UI; diagnostic logs stay server-side.
		message = "邮件发送失败，请检查 SMTP 配置或稍后重试"
		if job.Attempts >= maxNotificationAttempts {
			failed = now
		} else {
			next = now + 30*(1<<min(job.Attempts-1, 7))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := r.db.DB.ExecContext(ctx, r.db.Bind(`UPDATE notification_jobs SET claim_token='',claimed_until=0,
completed_at=?,failed_at=?,next_attempt_at=?,last_error=? WHERE comment_id=? AND kind=? AND claim_token=?`), completed, failed, next, message, job.CommentID, job.Kind, job.ClaimToken)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err == nil && affected != 1 {
		return fmt.Errorf("notification task lease was lost")
	}
	return err
}

func (r *Repository) RetryNotification(commentID int64, kind string) error {
	if kind != "admin" && kind != "reply" {
		return fmt.Errorf("无效的通知类型")
	}
	result, err := r.db.Exec(`UPDATE notification_jobs SET attempts=0,failed_at=0,next_attempt_at=0,last_error=''
WHERE comment_id=? AND kind=? AND completed_at=0 AND failed_at>0 AND claimed_until<=?`, commentID, kind, time.Now().Unix())
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err == nil && affected != 1 {
		return sql.ErrNoRows
	}
	return err
}

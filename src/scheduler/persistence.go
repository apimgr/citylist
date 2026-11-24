package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/apimgr/citylist/src/database"
)

// NewPersistent creates a scheduler backed by the application's scheduler tables.
func NewPersistent(store *database.Store, catchUpWindow time.Duration) (*Scheduler, error) {
	if store == nil {
		return nil, errors.New("scheduler database is required")
	}
	if catchUpWindow < 0 {
		return nil, errors.New("scheduler catch-up window must not be negative")
	}
	s := New()
	s.store = store
	s.catchUpWindow = catchUpWindow
	return s, nil
}

func (s *Scheduler) restoreTask(task *Task) error {
	if s.store == nil {
		return nil
	}
	var enabled int
	var storedSchedule string
	var lastRun, nextRun sql.NullInt64
	var status, lastError sql.NullString
	var runCount, failCount uint64
	err := s.store.Read(context.Background(), func(ctx context.Context, reader database.Reader) error {
		return reader.QueryRowContext(ctx, `SELECT enabled, schedule, last_run, next_run, last_status, last_error, run_count, fail_count FROM scheduler_tasks WHERE id = ?`, task.Name).
			Scan(&enabled, &storedSchedule, &lastRun, &nextRun, &status, &lastError, &runCount, &failCount)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return s.store.Write(context.Background(), func(tx *sql.Tx) error {
			_, err := tx.ExecContext(context.Background(), `INSERT INTO scheduler_tasks (id, name, enabled, schedule, next_run) VALUES (?, ?, ?, ?, ?)`, task.Name, task.Name, task.Enabled, task.Schedule, task.NextRun.Unix())
			return err
		})
	}
	if err != nil {
		return fmt.Errorf("load scheduler task %q: %w", task.Name, err)
	}
	now := time.Now().In(task.Location)
	configuredSchedule := task.Schedule
	task.Enabled = enabled != 0
	task.Schedule = storedSchedule
	task.LastRun = timeFromUnix(lastRun)
	task.LastStatus = status.String
	task.LastError = lastError.String
	task.RunCount = runCount
	task.FailCount = failCount
	if nextRun.Valid {
		task.NextRun = time.Unix(nextRun.Int64, 0).In(task.Location)
	}
	if configuredSchedule != storedSchedule {
		task.Schedule = configuredSchedule
		task.NextRun = nextTaskRun(task, now)
	}
	if task.LastStatus == "running" {
		task.LastStatus = "failed"
		task.LastError = "task was interrupted by a previous process"
		task.FailCount++
		// A process interruption is retried immediately after startup instead
		// of waiting for the next regular schedule.
		task.NextRun = now
		if err := s.store.Write(context.Background(), func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(context.Background(), `UPDATE scheduler_tasks SET last_status = 'failed', last_error = ?, fail_count = ? WHERE id = ?`, task.LastError, task.FailCount, task.Name); err != nil {
				return err
			}
			_, err := tx.ExecContext(context.Background(), `UPDATE scheduler_history SET status = 'failed', finished_at = ?, error = ? WHERE task_id = ? AND status = 'running' AND finished_at IS NULL`, now.Unix(), task.LastError, task.Name)
			return err
		}); err != nil {
			return fmt.Errorf("recover interrupted scheduler task %q: %w", task.Name, err)
		}
	}
	if !task.NextRun.IsZero() && now.After(task.NextRun) && now.Sub(task.NextRun) > s.catchUpWindow {
		task.NextRun = nextTaskRun(task, now)
	}
	return s.store.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE scheduler_tasks SET name = ?, schedule = ?, next_run = ?, last_status = ?, last_error = ?, run_count = ?, fail_count = ? WHERE id = ?`, task.Name, task.Schedule, nullableUnix(task.NextRun), nullableString(task.LastStatus), nullableString(task.LastError), task.RunCount, task.FailCount, task.Name)
		return err
	})
}

func (s *Scheduler) recordTaskStart(task *Task, started time.Time) (int64, error) {
	if s.store == nil {
		return 0, nil
	}
	var historyID int64
	err := s.store.Write(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(context.Background(), `UPDATE scheduler_tasks SET last_status = 'running', last_error = NULL WHERE id = ?`, task.Name); err != nil {
			return err
		}
		result, err := tx.ExecContext(context.Background(), `INSERT INTO scheduler_history (task_id, started_at, status) VALUES (?, ?, 'running')`, task.Name, started.Unix())
		if err != nil {
			return err
		}
		historyID, err = result.LastInsertId()
		return err
	})
	return historyID, err
}

func (s *Scheduler) recordTaskFinish(task *Task, historyID int64, started, finished, nextRun time.Time, runErr error) error {
	if s.store == nil {
		return nil
	}
	status := "success"
	var errorValue any
	var runIncrement, failIncrement uint64 = 1, 0
	if runErr != nil {
		status = "failed"
		errorValue = runErr.Error()
		runIncrement, failIncrement = 0, 1
	}
	return s.store.Write(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(context.Background(), `UPDATE scheduler_tasks SET last_run = ?, next_run = ?, last_status = ?, last_error = ?, run_count = run_count + ?, fail_count = fail_count + ? WHERE id = ?`, finished.Unix(), nullableUnix(nextRun), status, errorValue, runIncrement, failIncrement, task.Name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(context.Background(), `UPDATE scheduler_history SET finished_at = ?, status = ?, error = ?, duration_ms = ? WHERE id = ?`, finished.Unix(), status, errorValue, finished.Sub(started).Milliseconds(), historyID); err != nil {
			return err
		}
		_, err := tx.ExecContext(context.Background(), `DELETE FROM scheduler_history WHERE task_id = ? AND id NOT IN (SELECT id FROM scheduler_history WHERE task_id = ? ORDER BY started_at DESC, id DESC LIMIT 100)`, task.Name, task.Name)
		return err
	})
}

func (s *Scheduler) persistEnabled(task *Task) error {
	if s.store == nil {
		return nil
	}
	return s.store.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE scheduler_tasks SET enabled = ?, next_run = ? WHERE id = ?`, task.Enabled, nullableUnix(task.NextRun), task.Name)
		return err
	})
}

func timeFromUnix(value sql.NullInt64) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return time.Unix(value.Int64, 0).UTC()
}

func nullableUnix(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.Unix()
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

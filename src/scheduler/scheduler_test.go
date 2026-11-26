package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/apimgr/citylist/src/database"
)

func schedulerTestDir(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve scheduler test source path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	metadata, err := os.ReadFile(filepath.Join(root, "IDEA.md"))
	if err != nil {
		t.Fatal(err)
	}
	value := func(key string) string {
		pattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `:\s*([^\s#]+)\s*$`)
		match := pattern.FindSubmatch(metadata)
		if len(match) != 2 {
			t.Fatalf("IDEA.md is missing %s", key)
		}
		return string(match[1])
	}
	base := filepath.Join(os.TempDir(), value("project_org"))
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(base, value("internal_name")+"-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}

func TestNew(t *testing.T) {
	s := New()
	if s == nil {
		t.Fatal("New() returned nil")
	}
	if len(s.GetTasks()) != 0 {
		t.Errorf("new scheduler should have 0 tasks, got %d", len(s.GetTasks()))
	}
}

func TestAddTask(t *testing.T) {
	s := New()
	s.AddTask("task1", time.Hour, func() error { return nil })

	tasks := s.GetTasks()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Name != "task1" {
		t.Errorf("task name = %q, want task1", tasks[0].Name)
	}
	if !tasks[0].Enabled {
		t.Error("expected new task to be enabled by default")
	}
}

func TestAddScheduledTask(t *testing.T) {
	s := New()
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddScheduledTask("weekly", "0 3 * * 0", false, location, func() error { return nil }); err != nil {
		t.Fatalf("AddScheduledTask() error: %v", err)
	}
	tasks := s.GetTasks()
	if len(tasks) != 1 || tasks[0].Schedule != "0 3 * * 0" || tasks[0].Enabled || tasks[0].NextRun.IsZero() {
		t.Fatalf("scheduled task = %+v", tasks)
	}
	if err := s.AddScheduledTask("bad", "not-a-schedule", true, location, func() error { return nil }); err == nil {
		t.Fatal("AddScheduledTask() accepted an invalid schedule")
	}
}

func TestRemoveTask(t *testing.T) {
	s := New()
	s.AddTask("task1", time.Hour, func() error { return nil })
	s.RemoveTask("task1")

	if len(s.GetTasks()) != 0 {
		t.Errorf("expected 0 tasks after removal, got %d", len(s.GetTasks()))
	}
}

func TestEnableDisableTask(t *testing.T) {
	s := New()
	s.AddTask("task1", time.Hour, func() error { return nil })

	if err := s.DisableTask("task1"); err != nil {
		t.Fatal(err)
	}
	tasks := s.GetTasks()
	if tasks[0].Enabled {
		t.Error("expected task to be disabled")
	}

	if err := s.EnableTask("task1"); err != nil {
		t.Fatal(err)
	}
	tasks = s.GetTasks()
	if !tasks[0].Enabled {
		t.Error("expected task to be enabled")
	}

	// operating on a nonexistent task must not panic
	if err := s.EnableTask("nonexistent"); err == nil {
		t.Error("expected enabling a nonexistent task to fail")
	}
	if err := s.DisableTask("nonexistent"); err == nil {
		t.Error("expected disabling a nonexistent task to fail")
	}
}

func TestRunNow(t *testing.T) {
	s := New()

	var ran int32
	s.AddTask("task1", time.Hour, func() error {
		atomic.AddInt32(&ran, 1)
		return nil
	})

	if err := s.RunNow("task1"); err != nil {
		t.Fatalf("RunNow() error: %v", err)
	}
	if atomic.LoadInt32(&ran) != 1 {
		t.Errorf("expected task to run once, ran %d times", ran)
	}

	tasks := s.GetTasks()
	if tasks[0].LastRun.IsZero() {
		t.Error("expected LastRun to be set after RunNow")
	}
}

func TestRunNowAsyncTracksImmediateTaskAndRejectsInvalidRequests(t *testing.T) {
	s := New()
	started, release, completed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	s.AddTask("async", time.Hour, func() error {
		close(started)
		<-release
		close(completed)
		return nil
	})
	if err := s.RunNowAsync("async"); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("RunNowAsync before Start error = %v", err)
	}
	s.Start()
	if err := s.RunNowAsync("missing"); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("RunNowAsync missing task error = %v", err)
	}
	if err := s.RunNowAsync("async"); err != nil {
		t.Fatalf("RunNowAsync() error: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("asynchronous task did not start")
	}
	if err := s.RunNowAsync("async"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("duplicate RunNowAsync error = %v", err)
	}
	close(release)
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("asynchronous task did not finish")
	}
	s.Stop()
	if tasks := s.GetTasks(); len(tasks) != 1 || tasks[0].LastStatus != "success" {
		t.Fatalf("asynchronous task status = %+v, want successful completion", tasks)
	}
}

func TestRunNowNonexistent(t *testing.T) {
	s := New()
	if err := s.RunNow("nonexistent"); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Errorf("RunNow() on nonexistent task error = %v, want not registered", err)
	}
}

func TestRunNowError(t *testing.T) {
	s := New()
	wantErr := errors.New("task failed")
	s.AddTask("task1", time.Hour, func() error { return wantErr })

	if err := s.RunNow("task1"); !errors.Is(err, wantErr) {
		t.Errorf("RunNow() error = %v, want %v", err, wantErr)
	}
}

func TestRetryPolicySchedulesExponentialRetry(t *testing.T) {
	s := New()
	s.AddTask("task1", time.Hour, func() error { return errors.New("temporary failure") })
	if err := s.SetRetryPolicy("task1", true, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.RunNow("task1"); err == nil {
		t.Fatal("RunNow() should return the task failure")
	}
	task := s.GetTasks()[0]
	if task.NextRun.Before(time.Now().Add(59*time.Second)) || task.NextRun.After(time.Now().Add(61*time.Second)) {
		t.Fatalf("first retry is scheduled at %s, want about one minute from now", task.NextRun)
	}
	if task.FailCount != 1 || task.LastStatus != "failed" {
		t.Fatalf("retry state = %+v", task)
	}
}

func TestFailureHookReceivesTaskErrors(t *testing.T) {
	s := New()
	wantErr := errors.New("scheduled failure")
	var reportedName string
	var reportedErr error
	s.SetFailureHook(func(name string, err error) {
		reportedName, reportedErr = name, err
	})
	s.AddTask("task1", time.Hour, func() error { return wantErr })
	if err := s.RunNow("task1"); !errors.Is(err, wantErr) {
		t.Fatalf("RunNow() error = %v, want %v", err, wantErr)
	}
	if reportedName != "task1" || !errors.Is(reportedErr, wantErr) {
		t.Fatalf("failure hook received (%q, %v), want (task1, %v)", reportedName, reportedErr, wantErr)
	}
}

func TestSetRetryPolicyValidation(t *testing.T) {
	s := New()
	s.AddTask("task1", time.Hour, func() error { return nil })
	if err := s.SetRetryPolicy("task1", true, 0); err == nil {
		t.Fatal("SetRetryPolicy() accepted a zero delay")
	}
	if err := s.SetRetryPolicy("missing", true, time.Minute); err == nil {
		t.Fatal("SetRetryPolicy() accepted an unknown task")
	}
}

func TestRetryPolicyStopsAfterThreeRetries(t *testing.T) {
	s := New()
	s.AddTask("task1", time.Hour, func() error { return errors.New("persistent failure") })
	if err := s.SetRetryPolicy("task1", true, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 4; attempt++ {
		if err := s.RunNow("task1"); err == nil {
			t.Fatalf("attempt %d unexpectedly succeeded", attempt+1)
		}
	}
	task := s.GetTasks()[0]
	if task.FailCount != 4 || task.NextRun.Before(time.Now().Add(59*time.Minute)) {
		t.Fatalf("retry limit state = %+v", task)
	}
}

func TestRunNowPanicIsRecordedAndContained(t *testing.T) {
	s := New()
	s.AddTask("task1", time.Hour, func() error { panic("broken task") })

	if err := s.RunNow("task1"); err == nil || err.Error() != "task panic: broken task" {
		t.Fatalf("RunNow() panic error = %v, want recovered task panic", err)
	}
	tasks := s.GetTasks()
	if len(tasks) != 1 || tasks[0].LastStatus != "failed" || tasks[0].FailCount != 1 || tasks[0].LastError != "task panic: broken task" {
		t.Fatalf("panicked task state = %+v", tasks)
	}
	if tasks[0].NextRun.IsZero() {
		t.Fatal("panicked task did not retain its next scheduled run")
	}
}

func TestStartStop(t *testing.T) {
	s := New()
	s.AddTask("task1", time.Hour, func() error { return nil })

	s.Start()
	// starting again while already running should be a no-op, not panic
	s.Start()
	s.Stop()
	// stopping again while already stopped should be a no-op, not panic
	s.Stop()
}

func TestGetTasks(t *testing.T) {
	s := New()
	s.AddTask("task1", time.Hour, func() error { return nil })
	s.AddTask("task2", 2*time.Hour, func() error { return nil })

	tasks := s.GetTasks()
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
}

func TestParseInterval(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"minutely", time.Minute},
		{"hourly", time.Hour},
		{"daily", 24 * time.Hour},
		{"weekly", 7 * 24 * time.Hour},
		{"monthly", 30 * 24 * time.Hour},
		{"5m", 5 * time.Minute},
		{"garbage", 24 * time.Hour},
	}
	for _, tt := range tests {
		if got := ParseInterval(tt.in); got != tt.want {
			t.Errorf("ParseInterval(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestValidateSchedule(t *testing.T) {
	valid := []string{
		"0 2 * * *", "0 3 * * 0", "*/15 * * * *", "1-5/2 0,12 * jan-dec mon-fri",
		"@hourly", "@daily", "@weekly", "@monthly", "@every 15m", "@every 2h",
	}
	for _, expression := range valid {
		if err := ValidateSchedule(expression); err != nil {
			t.Errorf("ValidateSchedule(%q) error: %v", expression, err)
		}
	}
	invalid := []string{
		"", "@yearly", "@every 0m", "@every 5s", "@every badm", "* * * *", "60 * * * *",
		"0 24 * * *", "0 0 32 * *", "0 0 * 13 *", "0 0 * * 8", "*/0 * * * *", "5-1 * * * *", "1,,2 * * * *",
	}
	for _, expression := range invalid {
		if err := ValidateSchedule(expression); err == nil {
			t.Errorf("ValidateSchedule(%q) succeeded, want error", expression)
		}
	}
}

func TestNextOccurrence(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		expression string
		after      time.Time
		want       time.Time
	}{
		{
			name:       "daily cron",
			expression: "0 3 * * *",
			after:      time.Date(2025, time.January, 2, 2, 59, 0, 0, location),
			want:       time.Date(2025, time.January, 2, 3, 0, 0, 0, location),
		},
		{
			name:       "weekly named weekday",
			expression: "0 3 * * sun",
			after:      time.Date(2025, time.January, 4, 4, 0, 0, 0, location),
			want:       time.Date(2025, time.January, 5, 3, 0, 0, 0, location),
		},
		{
			name:       "monthly alias",
			expression: "@monthly",
			after:      time.Date(2025, time.January, 31, 12, 0, 0, 0, location),
			want:       time.Date(2025, time.February, 1, 0, 0, 0, 0, location),
		},
		{
			name:       "duration interval",
			expression: "@every 15m",
			after:      time.Date(2025, time.January, 2, 2, 59, 0, 0, location),
			want:       time.Date(2025, time.January, 2, 3, 14, 0, 0, location),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NextOccurrence(test.expression, test.after, location)
			if err != nil {
				t.Fatalf("NextOccurrence() error: %v", err)
			}
			if !got.Equal(test.want) {
				t.Fatalf("NextOccurrence() = %s, want %s", got, test.want)
			}
		})
	}
}

func TestPersistentSchedulerRestoresAndRecordsTaskState(t *testing.T) {
	store, err := database.OpenSQLite(filepath.Join(schedulerTestDir(t), "db", "server.db"), database.DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}

	s, err := NewPersistent(store, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddScheduledTask("persisted", "@every 1h", true, time.UTC, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.RunNow("persisted"); err != nil {
		t.Fatal(err)
	}
	var status string
	var runCount, failCount int
	var historyCount int
	if err := store.Read(context.Background(), func(ctx context.Context, reader database.Reader) error {
		if err := reader.QueryRowContext(ctx, `SELECT last_status, run_count, fail_count FROM scheduler_tasks WHERE id = ?`, "persisted").Scan(&status, &runCount, &failCount); err != nil {
			return err
		}
		return reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduler_history WHERE task_id = ? AND status = 'success' AND finished_at IS NOT NULL`, "persisted").Scan(&historyCount)
	}); err != nil {
		t.Fatal(err)
	}
	if status != "success" || runCount != 1 || failCount != 0 || historyCount != 1 {
		t.Fatalf("persisted result status=%q runs=%d failures=%d history=%d", status, runCount, failCount, historyCount)
	}

	if err := s.DisableTask("persisted"); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPersistent(store, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.AddScheduledTask("persisted", "@every 1h", true, time.UTC, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	tasks := restarted.GetTasks()
	if len(tasks) != 1 || tasks[0].Enabled || tasks[0].RunCount != 1 || tasks[0].LastStatus != "success" {
		t.Fatalf("restored task = %+v", tasks)
	}
	if err := restarted.RunNow("persisted"); err == nil {
		t.Fatal("RunNow accepted a disabled persisted task")
	}

	if err := store.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE scheduler_tasks SET last_status = 'running' WHERE id = ?`, "persisted")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	recovered, err := NewPersistent(store, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := recovered.AddScheduledTask("persisted", "@every 1h", true, time.UTC, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if tasks := recovered.GetTasks(); len(tasks) != 1 || tasks[0].LastStatus != "failed" || tasks[0].FailCount != 1 {
		t.Fatalf("interrupted task recovery = %+v", tasks)
	}
	if tasks := recovered.GetTasks(); time.Until(tasks[0].NextRun) > time.Second {
		t.Fatalf("interrupted task next run = %s, want immediate retry", tasks[0].NextRun)
	}
}

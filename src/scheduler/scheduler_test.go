package scheduler

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

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

	s.DisableTask("task1")
	tasks := s.GetTasks()
	if tasks[0].Enabled {
		t.Error("expected task to be disabled")
	}

	s.EnableTask("task1")
	tasks = s.GetTasks()
	if !tasks[0].Enabled {
		t.Error("expected task to be enabled")
	}

	// operating on a nonexistent task must not panic
	s.EnableTask("nonexistent")
	s.DisableTask("nonexistent")
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

func TestRunNowNonexistent(t *testing.T) {
	s := New()
	if err := s.RunNow("nonexistent"); err != nil {
		t.Errorf("RunNow() on nonexistent task should return nil, got %v", err)
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

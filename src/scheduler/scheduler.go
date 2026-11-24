package scheduler

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/apimgr/citylist/src/database"
	"github.com/apimgr/citylist/src/server/metrics"
)

var cronRanges = [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
var cronNames = [][]string{
	nil,
	nil,
	nil,
	{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"},
	{"sun", "mon", "tue", "wed", "thu", "fri", "sat"},
}

// ValidateSchedule checks the cron and interval forms supported by AI.md PART 18.
func ValidateSchedule(expression string) error {
	expression = strings.TrimSpace(expression)
	switch expression {
	case "@hourly", "@daily", "@weekly", "@monthly":
		return nil
	}
	if strings.HasPrefix(expression, "@every ") {
		interval := strings.TrimSpace(strings.TrimPrefix(expression, "@every "))
		if len(interval) < 2 || (interval[len(interval)-1] != 'm' && interval[len(interval)-1] != 'h') {
			return fmt.Errorf("interval must use minutes or hours")
		}
		amount, err := strconv.Atoi(interval[:len(interval)-1])
		if err != nil || amount <= 0 {
			return fmt.Errorf("interval must be a positive number of minutes or hours")
		}
		return nil
	}
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return fmt.Errorf("cron schedule must have five fields")
	}
	for index, field := range fields {
		if err := validateCronField(field, cronRanges[index][0], cronRanges[index][1], cronNames[index]); err != nil {
			return fmt.Errorf("invalid cron field %d: %w", index+1, err)
		}
	}
	return nil
}

// NextOccurrence returns the first scheduled time strictly after after.
func NextOccurrence(expression string, after time.Time, location *time.Location) (time.Time, error) {
	if err := ValidateSchedule(expression); err != nil {
		return time.Time{}, err
	}
	if location == nil {
		return time.Time{}, fmt.Errorf("scheduler timezone is required")
	}
	if strings.HasPrefix(expression, "@every ") {
		interval := strings.TrimSpace(strings.TrimPrefix(expression, "@every "))
		amount, _ := strconv.Atoi(interval[:len(interval)-1])
		unit := time.Minute
		if interval[len(interval)-1] == 'h' {
			unit = time.Hour
		}
		return after.In(location).Add(time.Duration(amount) * unit), nil
	}
	fields := strings.Fields(expression)
	switch expression {
	case "@hourly":
		fields = strings.Fields("0 * * * *")
	case "@daily":
		fields = strings.Fields("0 0 * * *")
	case "@weekly":
		fields = strings.Fields("0 0 * * 0")
	case "@monthly":
		fields = strings.Fields("0 0 1 * *")
	}
	start := after.In(location).Truncate(time.Minute).Add(time.Minute)
	limit := start.AddDate(5, 0, 0)
	for candidate := start; candidate.Before(limit); candidate = candidate.Add(time.Minute) {
		if matchesSchedule(fields, candidate) {
			return candidate, nil
		}
	}
	return time.Time{}, fmt.Errorf("no occurrence found within five years")
}

func matchesSchedule(fields []string, candidate time.Time) bool {
	values := []int{candidate.Minute(), candidate.Hour(), candidate.Day(), int(candidate.Month()), int(candidate.Weekday())}
	for index := 0; index < 2; index++ {
		if !cronFieldMatches(fields[index], values[index], cronRanges[index][0], cronRanges[index][1], cronNames[index]) {
			return false
		}
	}
	if !cronFieldMatches(fields[3], values[3], cronRanges[3][0], cronRanges[3][1], cronNames[3]) {
		return false
	}
	dayOfMonth := cronFieldMatches(fields[2], values[2], cronRanges[2][0], cronRanges[2][1], cronNames[2])
	dayOfWeek := cronFieldMatches(fields[4], values[4], cronRanges[4][0], cronRanges[4][1], cronNames[4])
	if fields[2] != "*" && fields[4] != "*" {
		return dayOfMonth || dayOfWeek
	}
	return dayOfMonth && dayOfWeek
}

func cronFieldMatches(field string, value, minimum, maximum int, names []string) bool {
	for _, item := range strings.Split(field, ",") {
		base := item
		step := 1
		if parts := strings.Split(item, "/"); len(parts) == 2 {
			base = parts[0]
			step, _ = strconv.Atoi(parts[1])
		}
		start, end := minimum, maximum
		if base != "*" {
			parts := strings.Split(base, "-")
			var err error
			start, err = cronValue(parts[0], minimum, maximum, names)
			if err != nil {
				continue
			}
			end = start
			if len(parts) == 2 {
				end, err = cronValue(parts[1], minimum, maximum, names)
				if err != nil {
					continue
				}
			} else if strings.Contains(item, "/") {
				end = maximum
			}
		}
		if value >= start && value <= end && (value-start)%step == 0 {
			return true
		}
		if maximum == 7 && value == 0 && end == 7 && start <= 7 && (7-start)%step == 0 {
			return true
		}
	}
	return false
}

func validateCronField(field string, minimum, maximum int, names []string) error {
	if field == "" {
		return fmt.Errorf("field is empty")
	}
	for _, item := range strings.Split(field, ",") {
		if item == "" {
			return fmt.Errorf("list item is empty")
		}
		base := item
		if parts := strings.Split(item, "/"); len(parts) == 2 {
			base = parts[0]
			parsed, err := strconv.Atoi(parts[1])
			if err != nil || parsed <= 0 {
				return fmt.Errorf("step must be a positive integer")
			}
		} else if strings.Contains(item, "/") {
			return fmt.Errorf("field has more than one step separator")
		}
		if base == "*" {
			continue
		}
		parts := strings.Split(base, "-")
		if len(parts) > 2 || parts[0] == "" || (len(parts) == 2 && parts[1] == "") {
			return fmt.Errorf("invalid range")
		}
		start, err := cronValue(parts[0], minimum, maximum, names)
		if err != nil {
			return err
		}
		end := start
		if len(parts) == 2 {
			end, err = cronValue(parts[1], minimum, maximum, names)
			if err != nil {
				return err
			}
		} else if strings.Contains(item, "/") {
			end = maximum
		}
		if start > end {
			return fmt.Errorf("range start exceeds range end")
		}
	}
	return nil
}

func cronValue(value string, minimum, maximum int, names []string) (int, error) {
	if len(names) > 0 {
		for index, name := range names {
			if strings.EqualFold(value, name) {
				return minimum + index, nil
			}
		}
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("value %q is outside %d-%d", value, minimum, maximum)
	}
	return parsed, nil
}

// Task represents a scheduled task
type Task struct {
	Name        string
	Interval    time.Duration
	Schedule    string
	Location    *time.Location
	Func        func() error
	RetryOnFail bool
	RetryDelay  time.Duration
	retryCount  int
	LastRun     time.Time
	NextRun     time.Time
	Enabled     bool
	LastStatus  string
	LastError   string
	RunCount    uint64
	FailCount   uint64
	running     bool
}

// SetRetryPolicy configures bounded exponential retries for a registered task.
func (s *Scheduler) SetRetryPolicy(name string, enabled bool, delay time.Duration) error {
	if enabled && delay <= 0 {
		return fmt.Errorf("retry delay must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[name]
	if !ok {
		return fmt.Errorf("task %q is not registered", name)
	}
	task.RetryOnFail = enabled
	task.RetryDelay = delay
	return nil
}

// Scheduler manages periodic tasks
type Scheduler struct {
	tasks         map[string]*Task
	stop          chan struct{}
	running       bool
	store         *database.Store
	catchUpWindow time.Duration
	failureHook   func(string, error)
	taskWG        sync.WaitGroup
	mu            sync.RWMutex
}

// New creates a new scheduler
func New() *Scheduler {
	return &Scheduler{
		tasks: make(map[string]*Task),
		stop:  make(chan struct{}),
	}
}

// SetFailureHook registers an observer for task failures.
func (s *Scheduler) SetFailureHook(hook func(string, error)) {
	s.mu.Lock()
	s.failureHook = hook
	s.mu.Unlock()
}

// AddTask adds a new task to the scheduler
func (s *Scheduler) AddTask(name string, interval time.Duration, fn func() error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tasks[name] = &Task{
		Name:     name,
		Interval: interval,
		Func:     fn,
		NextRun:  time.Now().Add(interval),
		Enabled:  true,
	}
}

// AddScheduledTask registers a cron or interval expression in a specific timezone.
func (s *Scheduler) AddScheduledTask(name, expression string, enabled bool, location *time.Location, fn func() error) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("task name is required")
	}
	if location == nil {
		return fmt.Errorf("task timezone is required")
	}
	if err := ValidateSchedule(expression); err != nil {
		return fmt.Errorf("invalid schedule for task %q: %w", name, err)
	}
	nextRun, err := NextOccurrence(expression, time.Now(), location)
	if err != nil {
		return fmt.Errorf("calculate next run for task %q: %w", name, err)
	}
	task := &Task{Name: name, Schedule: expression, Location: location, Func: fn, NextRun: nextRun, Enabled: enabled}
	if err := s.restoreTask(task); err != nil {
		return err
	}
	s.mu.Lock()
	s.tasks[name] = task
	s.mu.Unlock()
	return nil
}

// RemoveTask removes a task from the scheduler
func (s *Scheduler) RemoveTask(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tasks, name)
}

// EnableTask enables a task
func (s *Scheduler) EnableTask(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if task, ok := s.tasks[name]; ok {
		wasEnabled, previousNextRun := task.Enabled, task.NextRun
		task.Enabled = true
		task.NextRun = nextTaskRun(task, time.Now())
		if err := s.persistEnabled(task); err != nil {
			task.Enabled, task.NextRun = wasEnabled, previousNextRun
			return fmt.Errorf("persist enabled state for task %q: %w", name, err)
		}
		return nil
	}
	return fmt.Errorf("task %q is not registered", name)
}

// DisableTask disables a task
func (s *Scheduler) DisableTask(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if task, ok := s.tasks[name]; ok {
		wasEnabled := task.Enabled
		task.Enabled = false
		if err := s.persistEnabled(task); err != nil {
			task.Enabled = wasEnabled
			return fmt.Errorf("persist enabled state for task %q: %w", name, err)
		}
		return nil
	}
	return fmt.Errorf("task %q is not registered", name)
}

// Start begins the scheduler loop
func (s *Scheduler) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stop = make(chan struct{})
	stop := s.stop
	names := make([]string, 0, len(s.tasks))
	for name, task := range s.tasks {
		if task.Enabled {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	s.mu.Unlock()
	slog.Info("scheduler.started", "tasks", strings.Join(names, ", "))
	s.runDueTasks()

	go func() {
		ticker := time.NewTicker(30 * time.Second) // Check every 30 seconds
		defer ticker.Stop()

		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.runDueTasks()
			}
		}
	}()
}

// Stop stops the scheduler
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	close(s.stop)
	s.running = false
	s.mu.Unlock()
	done := make(chan struct{})
	go func() {
		s.taskWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		slog.Warn("scheduler.shutdown_timeout", "timeout", 30*time.Second)
	}
}

// runDueTasks executes tasks that are due
func (s *Scheduler) runDueTasks() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	now := time.Now()
	dueTasks := make([]*Task, 0)

	for _, task := range s.tasks {
		if task.Enabled && !task.running && !now.Before(task.NextRun) {
			task.running = true
			s.taskWG.Add(1)
			dueTasks = append(dueTasks, task)
		}
	}
	sort.Slice(dueTasks, func(i, j int) bool { return dueTasks[i].NextRun.Before(dueTasks[j].NextRun) })
	s.mu.Unlock()

	for _, task := range dueTasks {
		go func(t *Task) {
			defer s.taskWG.Done()
			_ = s.executeTask(t)
		}(task)
	}
}

// RunNow immediately runs a task by name
func (s *Scheduler) RunNow(name string) error {
	s.mu.Lock()
	task, ok := s.tasks[name]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("task %q is not registered", name)
	}
	if !task.Enabled {
		s.mu.Unlock()
		return fmt.Errorf("task %q is disabled", name)
	}
	if task.running {
		s.mu.Unlock()
		return fmt.Errorf("task %q is already running", name)
	}
	task.running = true
	s.taskWG.Add(1)
	s.mu.Unlock()
	defer s.taskWG.Done()
	return s.executeTask(task)
}

// RunNowAsync schedules an immediate task run without blocking the caller.
func (s *Scheduler) RunNowAsync(name string) error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return errors.New("scheduler is not running")
	}
	task, ok := s.tasks[name]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("task %q is not registered", name)
	}
	if !task.Enabled {
		s.mu.Unlock()
		return fmt.Errorf("task %q is disabled", name)
	}
	if task.running {
		s.mu.Unlock()
		return fmt.Errorf("task %q is already running", name)
	}
	task.running = true
	s.taskWG.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.taskWG.Done()
		_ = s.executeTask(task)
	}()
	return nil
}

func (s *Scheduler) executeTask(task *Task) error {
	started := time.Now()
	metricStarted := recordTaskMetricStart(task.Name)
	defer func() {
		if metricStarted {
			s.mu.RLock()
			failed := task.LastStatus == "failed"
			s.mu.RUnlock()
			recordTaskMetricEnd(task.Name, time.Since(started), failed)
		}
	}()
	slog.Debug("scheduler.task_started", "task", task.Name)
	historyID, err := s.recordTaskStart(task, started)
	if err != nil {
		s.mu.Lock()
		task.running = false
		task.NextRun = nextTaskRun(task, time.Now())
		task.LastStatus = "failed"
		task.LastError = fmt.Sprintf("persist task start: %v", err)
		task.FailCount++
		failureHook := s.failureHook
		s.mu.Unlock()
		slog.Warn("scheduler.task_failed", "task", task.Name, "error", err)
		if failureHook != nil {
			failureHook(task.Name, err)
		}
		return err
	}
	runErr := invoke(task.Func)
	finished := time.Now()
	retriesExhausted := false
	s.mu.Lock()
	task.LastRun = finished
	task.NextRun = nextTaskRun(task, finished)
	if runErr != nil {
		task.LastStatus = "failed"
		task.LastError = runErr.Error()
		task.FailCount++
		if task.RetryOnFail && task.retryCount < 3 {
			task.NextRun = finished.Add(task.RetryDelay * time.Duration(1<<task.retryCount))
			task.retryCount++
		} else if task.RetryOnFail {
			retriesExhausted = true
		}
	} else {
		task.LastStatus = "success"
		task.LastError = ""
		task.RunCount++
		task.retryCount = 0
	}
	nextRun := task.NextRun
	failureHook := s.failureHook
	s.mu.Unlock()
	if err := s.recordTaskFinish(task, historyID, started, finished, nextRun, runErr); err != nil {
		slog.Error("scheduler.result_persist_failed", "task", task.Name, "error", err)
	}
	s.mu.Lock()
	task.running = false
	s.mu.Unlock()
	if runErr != nil {
		if failureHook != nil {
			failureHook(task.Name, runErr)
		}
		if retriesExhausted {
			slog.Error("scheduler.task_failed", "task", task.Name, "error", runErr)
		} else {
			slog.Warn("scheduler.task_failed", "task", task.Name, "error", runErr)
		}
	} else {
		slog.Debug("scheduler.task_completed", "task", task.Name)
	}
	return runErr
}

func recordTaskMetricStart(name string) bool {
	if metrics.SchedulerTasksRunning == nil {
		return false
	}
	metrics.SchedulerTasksRunning.WithLabelValues(name).Inc()
	return true
}

func recordTaskMetricEnd(name string, duration time.Duration, failed bool) {
	metrics.SchedulerTasksRunning.WithLabelValues(name).Dec()
	if metrics.SchedulerTaskDuration != nil {
		metrics.SchedulerTaskDuration.WithLabelValues(name).Observe(duration.Seconds())
	}
	if metrics.SchedulerLastRun != nil {
		metrics.SchedulerLastRun.WithLabelValues(name).SetToCurrentTime()
	}
	if metrics.SchedulerTasksTotal != nil {
		status := "success"
		if failed {
			status = "error"
		}
		metrics.SchedulerTasksTotal.WithLabelValues(name, status).Inc()
	}
}

func nextTaskRun(task *Task, after time.Time) time.Time {
	if task.Schedule != "" {
		next, err := NextOccurrence(task.Schedule, after, task.Location)
		if err == nil {
			return next
		}
		task.LastStatus = "failed"
		task.LastError = err.Error()
		task.FailCount++
		return time.Time{}
	}
	if task.Interval <= 0 {
		return time.Time{}
	}
	return after.Add(task.Interval)
}

func invoke(fn func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("task panic: %v", recovered)
		}
	}()
	if fn == nil {
		return fmt.Errorf("task function is nil")
	}
	return fn()
}

// GetTasks returns all registered tasks
func (s *Scheduler) GetTasks() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tasks := make([]Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		tasks = append(tasks, *t)
	}
	return tasks
}

// ParseInterval parses interval strings like "hourly", "daily", "weekly"
func ParseInterval(s string) time.Duration {
	switch s {
	case "minutely":
		return time.Minute
	case "hourly":
		return time.Hour
	case "daily":
		return 24 * time.Hour
	case "weekly":
		return 7 * 24 * time.Hour
	case "monthly":
		return 30 * 24 * time.Hour
	default:
		// Try to parse as duration
		if d, err := time.ParseDuration(s); err == nil {
			return d
		}
		return 24 * time.Hour // Default to daily
	}
}

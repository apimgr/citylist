package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/database"
	"github.com/apimgr/citylist/src/paths"
	"github.com/apimgr/citylist/src/scheduler"
)

type schedulerHistoryEntry struct {
	startedAt  int64
	finishedAt sql.NullInt64
	status     string
	errText    sql.NullString
	durationMS sql.NullInt64
}

func runSchedulerCommand(args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		fmt.Fprintln(stdout, schedulerFormat("scheduler_help", "Usage: {binary} scheduler {list|show <id>|run <id>|enable <id>|disable <id>|history <id>}", map[string]string{"binary": filepath.Base(os.Args[0])}))
		return nil
	}
	configDir, dataDir, logsDir := paths.GetDefaultDirs(projectName)
	if envConfig := os.Getenv("CONFIG_DIR"); envConfig != "" {
		configDir = envConfig
	}
	if envData := os.Getenv("DATA_DIR"); envData != "" {
		dataDir = envData
	}
	if envLogs := os.Getenv("LOGS_DIR"); envLogs != "" {
		logsDir = envLogs
	}
	for _, directory := range []string{configDir, dataDir, logsDir} {
		if err := paths.EnsurePrivateDir(directory); err != nil {
			return fmt.Errorf("%s", schedulerFormat("scheduler_directory_error", "could not secure runtime directory {path}: {error}", map[string]string{"path": directory, "error": err.Error()}))
		}
	}
	backupDir := paths.GetBackupDirFor(projectName, dataDir, "")
	configPath := filepath.Join(configDir, "server.yml")
	if _, err := os.Stat(configPath); err != nil {
		if os.IsNotExist(err) {
			return errors.New(schedulerFormat("scheduler_config_missing", "configuration file not found: {path}", map[string]string{"path": configPath}))
		}
		return err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("%s", schedulerFormat("scheduler_config_error", "could not load configuration: {error}", map[string]string{"error": err.Error()}))
	}
	if err := configureLogs(logsDir, cfg.Server.Mode, cfg.Server.Logs); err != nil {
		return fmt.Errorf("%s", schedulerFormat("scheduler_logging_error", "could not configure task logging: {error}", map[string]string{"error": err.Error()}))
	}
	store, err := openConfiguredDatabase(cfg, dataDir)
	if err != nil {
		return fmt.Errorf("%s", schedulerFormat("scheduler_database_error", "could not open database: {error}", map[string]string{"error": err.Error()}))
	}
	defer func() { _ = store.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := store.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("%s", schedulerFormat("scheduler_schema_error", "could not initialize database schema: {error}", map[string]string{"error": err.Error()}))
	}
	appScheduler, err := newApplicationScheduler(cfg, configDir, dataDir, logsDir, backupDir, store, ctx, nil)
	if err != nil {
		return fmt.Errorf("%s", schedulerFormat("scheduler_init_error", "could not initialize scheduler: {error}", map[string]string{"error": err.Error()}))
	}
	return executeSchedulerCommand(args, stdout, appScheduler, store)
}

func executeSchedulerCommand(args []string, stdout io.Writer, appScheduler *scheduler.Scheduler, store *database.Store) error {
	if len(args) == 0 {
		return errors.New(schedulerFormat("scheduler_usage", "scheduler command is required; run '{binary} scheduler --help'", map[string]string{"binary": filepath.Base(os.Args[0])}))
	}
	action := args[0]
	if action == "--help" || action == "help" {
		fmt.Fprintln(stdout, schedulerFormat("scheduler_help", "Usage: {binary} scheduler {list|show <id>|run <id>|enable <id>|disable <id>|history <id>}", map[string]string{"binary": filepath.Base(os.Args[0])}))
		return nil
	}
	if action == "list" {
		if len(args) != 1 {
			return errors.New(schedulerMessage("scheduler_list_usage", "usage: citylist scheduler list"))
		}
		return printSchedulerList(stdout, appScheduler.GetTasks())
	}
	if action != "show" && action != "history" && action != "run" && action != "enable" && action != "disable" {
		return fmt.Errorf("%s", schedulerFormat("scheduler_unknown_command", "unknown scheduler command {command}", map[string]string{"command": action}))
	}
	if len(args) != 2 || strings.TrimSpace(args[1]) == "" {
		return errors.New(schedulerMessage("scheduler_task_usage", "a task ID is required"))
	}
	id := args[1]
	var task *scheduler.Task
	for _, current := range appScheduler.GetTasks() {
		if current.Name == id {
			copy := current
			task = &copy
			break
		}
	}
	if task == nil {
		return fmt.Errorf("%s", schedulerFormat("scheduler_unknown_task", "task {id} is not registered", map[string]string{"id": id}))
	}
	switch action {
	case "show":
		return printSchedulerDetails(stdout, *task)
	case "history":
		return printSchedulerHistory(stdout, store, id)
	case "run":
		if err := appScheduler.RunNow(id); err != nil {
			return fmt.Errorf("%s", schedulerFormat("scheduler_run_error", "could not run {id}: {error}", map[string]string{"id": id, "error": err.Error()}))
		}
		fmt.Fprintln(stdout, schedulerFormat("scheduler_run_complete", "task {id} completed successfully", map[string]string{"id": id}))
		return nil
	case "enable":
		if err := appScheduler.EnableTask(id); err != nil {
			return err
		}
		fmt.Fprintln(stdout, schedulerFormat("scheduler_enabled", "enabled task {id}", map[string]string{"id": id}))
		return nil
	case "disable":
		if err := appScheduler.DisableTask(id); err != nil {
			return err
		}
		fmt.Fprintln(stdout, schedulerFormat("scheduler_disabled", "disabled task {id}", map[string]string{"id": id}))
		return nil
	}
	return nil
}

func printSchedulerList(stdout io.Writer, tasks []scheduler.Task) error {
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Name < tasks[j].Name })
	if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\n", schedulerMessage("scheduler_column_task", "TASK"), schedulerMessage("scheduler_column_enabled", "ENABLED"), schedulerMessage("scheduler_column_schedule", "SCHEDULE"), schedulerMessage("scheduler_column_last_run", "LAST RUN"), schedulerMessage("scheduler_column_next_run", "NEXT RUN")); err != nil {
		return err
	}
	for _, task := range tasks {
		enabled := schedulerMessage("common_no", "no")
		if task.Enabled {
			enabled = schedulerMessage("common_yes", "yes")
		}
		if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\n", task.Name, enabled, task.Schedule, schedulerTime(task.LastRun), schedulerTime(task.NextRun)); err != nil {
			return err
		}
	}
	return nil
}

func printSchedulerDetails(stdout io.Writer, task scheduler.Task) error {
	status := task.LastStatus
	if status == "" {
		status = schedulerMessage("scheduler_status_pending", "pending")
	}
	enabled := schedulerMessage("common_no", "no")
	if task.Enabled {
		enabled = schedulerMessage("common_yes", "yes")
	}
	_, err := fmt.Fprintf(stdout, "%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n",
		schedulerFormat("scheduler_detail_title", "TASK: {id}", map[string]string{"id": task.Name}),
		schedulerFormat("scheduler_detail_enabled", "Enabled: {value}", map[string]string{"value": enabled}),
		schedulerFormat("scheduler_detail_status", "Status: {value}", map[string]string{"value": status}),
		schedulerFormat("scheduler_detail_schedule", "Schedule: {value}", map[string]string{"value": task.Schedule}),
		schedulerFormat("scheduler_detail_last_run", "Last run: {value}", map[string]string{"value": schedulerTime(task.LastRun)}),
		schedulerFormat("scheduler_detail_next_run", "Next run: {value}", map[string]string{"value": schedulerTime(task.NextRun)}),
		schedulerFormat("scheduler_detail_run_count", "Successful runs: {value}", map[string]string{"value": fmt.Sprint(task.RunCount)}),
		schedulerFormat("scheduler_detail_fail_count", "Failed runs: {value}", map[string]string{"value": fmt.Sprint(task.FailCount)}),
		schedulerFormat("scheduler_detail_last_error", "Last error: {value}", map[string]string{"value": task.LastError}),
	)
	return err
}

func printSchedulerHistory(stdout io.Writer, store *database.Store, taskID string) error {
	var history []schedulerHistoryEntry
	if err := store.Read(context.Background(), func(ctx context.Context, reader database.Reader) error {
		rows, err := reader.QueryContext(ctx, `SELECT started_at, finished_at, status, error, duration_ms FROM scheduler_history WHERE task_id = ? ORDER BY started_at DESC, id DESC LIMIT 100`, taskID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item schedulerHistoryEntry
			if err := rows.Scan(&item.startedAt, &item.finishedAt, &item.status, &item.errText, &item.durationMS); err != nil {
				return err
			}
			history = append(history, item)
		}
		return rows.Err()
	}); err != nil {
		return fmt.Errorf("%s", schedulerFormat("scheduler_history_error", "could not read history: {error}", map[string]string{"error": err.Error()}))
	}
	if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", schedulerMessage("scheduler_column_started", "STARTED"), schedulerMessage("scheduler_column_duration", "DURATION"), schedulerMessage("scheduler_column_status", "STATUS"), schedulerMessage("scheduler_column_error", "ERROR")); err != nil {
		return err
	}
	for _, item := range history {
		started := time.Unix(item.startedAt, 0).Local().Format(time.RFC3339)
		duration := "-"
		if item.durationMS.Valid {
			duration = (time.Duration(item.durationMS.Int64) * time.Millisecond).String()
		}
		errorText := item.errText.String
		if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", started, duration, item.status, errorText); err != nil {
			return err
		}
	}
	return nil
}

func schedulerTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Local().Format(time.RFC3339)
}

func schedulerMessage(key, fallback string) string {
	return serverOutputText(key, fallback)
}

func schedulerFormat(key, fallback string, values map[string]string) string {
	return serverOutputFormat(key, fallback, values)
}

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/citylist/src/client/api"
)

type cliLogger struct {
	mu       sync.Mutex
	path     string
	level    string
	maxSize  int64
	maxFiles int
}

var activeCLILogger *cliLogger

func configureCLILogger(settings cliConfig) error {
	maximum, err := parseSize(settings.Logging.MaxSize)
	if err != nil {
		return fmt.Errorf("parse logging.max_size: %w", err)
	}
	path := strings.TrimSpace(settings.Logging.File)
	if path == "" {
		path = filepath.Join(cliLogDirectory(), "cli.log")
	} else if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return fmt.Errorf("resolve logging.file: %w", homeErr)
		}
		path = filepath.Join(home, path[2:])
	}
	logger := &cliLogger{path: path, level: settings.Logging.Level, maxSize: maximum, maxFiles: settings.Logging.MaxFiles}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
			return fmt.Errorf("secure log directory: %w", err)
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open CLI log: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := file.Chmod(0600); err != nil {
			_ = file.Close()
			return fmt.Errorf("secure CLI log: %w", err)
		}
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close CLI log: %w", err)
	}
	activeCLILogger = logger
	api.SetLogHandler(logger.Log)
	return nil
}

func (l *cliLogger) Log(level, message string) {
	if l == nil || !l.enabled(level) {
		return
	}
	l.write(map[string]any{"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "level": level, "message": message})
}

func (l *cliLogger) Event(event string, fields map[string]string) {
	if l == nil {
		return
	}
	record := map[string]any{"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "event": event}
	for key, value := range fields {
		record[key] = value
	}
	l.write(record)
}

func (l *cliLogger) enabled(level string) bool {
	ranks := map[string]int{"debug": 0, "info": 1, "warn": 2, "error": 3}
	return ranks[level] >= ranks[l.level]
}

func (l *cliLogger) write(record map[string]any) {
	data, err := json.Marshal(record)
	if err != nil {
		return
	}
	data = append(data, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	info, err := os.Stat(l.path)
	if err == nil && info.Size()+int64(len(data)) > l.maxSize {
		if err := l.rotate(); err != nil {
			return
		}
	}
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	if runtime.GOOS != "windows" {
		_ = file.Chmod(0600)
	}
	_, _ = file.Write(data)
	_ = file.Close()
}

func (l *cliLogger) rotate() error {
	if l.maxFiles == 0 {
		if err := os.Remove(l.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	_ = os.Remove(fmt.Sprintf("%s.%d", l.path, l.maxFiles))
	for index := l.maxFiles - 1; index >= 1; index-- {
		oldPath := fmt.Sprintf("%s.%d", l.path, index)
		newPath := fmt.Sprintf("%s.%d", l.path, index+1)
		if err := os.Rename(oldPath, newPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(l.path, l.path+".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

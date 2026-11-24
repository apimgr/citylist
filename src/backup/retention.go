package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
)

type RetentionOptions struct {
	ProjectName string
	BackupDir   string
	Password    string
	MaxBackups  int
	KeepWeekly  int
	KeepMonthly int
	KeepYearly  int
	MaxTotal    string
	Now         time.Time
	OnDelete    func(string, string) error
}

type backupFile struct {
	path    string
	name    string
	date    time.Time
	size    int64
	modTime time.Time
	full    bool
	keep    bool
}

func ParseSize(value string) (int64, error) {
	value = strings.TrimSpace(strings.ToUpper(value))
	switch value {
	case "", "0", "FALSE", "NO", "NONE", "DISABLE", "DISABLED", "OFF":
		return 0, nil
	}
	if strings.HasSuffix(value, "%") {
		amount, err := strconv.ParseInt(strings.TrimSuffix(value, "%"), 10, 64)
		if err == nil && amount == 0 {
			return 0, nil
		}
		if err != nil || amount < 1 || amount > 100 {
			return 0, fmt.Errorf("invalid size percentage %q", value)
		}
		return -amount, nil
	}
	units := []struct {
		suffix string
		factor int64
	}{{"TB", 1 << 40}, {"T", 1 << 40}, {"GB", 1 << 30}, {"G", 1 << 30}, {"MB", 1 << 20}, {"M", 1 << 20}, {"KB", 1 << 10}, {"K", 1 << 10}, {"B", 1}}
	factor := int64(1)
	for _, unit := range units {
		if strings.HasSuffix(value, unit.suffix) {
			factor = unit.factor
			value = strings.TrimSpace(strings.TrimSuffix(value, unit.suffix))
			break
		}
	}
	amount, err := strconv.ParseInt(value, 10, 64)
	if err != nil || amount < 0 || amount > (1<<63-1)/factor {
		return 0, fmt.Errorf("invalid size %q", value)
	}
	return amount * factor, nil
}

func Prune(ctx context.Context, options RetentionOptions) ([]string, error) {
	if options.ProjectName == "" || options.BackupDir == "" {
		return nil, errors.New("retention project name and backup directory are required")
	}
	if options.MaxBackups < 1 {
		options.MaxBackups = 1
	}
	if options.KeepWeekly < 0 {
		options.KeepWeekly = 0
	}
	if options.KeepMonthly < 0 {
		options.KeepMonthly = 0
	}
	if options.KeepYearly < 0 {
		options.KeepYearly = 0
	}
	if options.Now.IsZero() {
		options.Now = time.Now().UTC()
	}
	maxTotal, err := ParseSize(options.MaxTotal)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(options.BackupDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	files := make([]backupFile, 0)
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, options.ProjectName+"_backup_") && !strings.HasPrefix(name, options.ProjectName+"-") {
			continue
		}
		if !strings.HasSuffix(name, ".tar.gz") && !strings.HasSuffix(name, ".tar.gz.enc") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		item := backupFile{path: filepath.Join(options.BackupDir, name), name: name, size: info.Size(), modTime: info.ModTime().UTC()}
		if strings.HasPrefix(name, options.ProjectName+"_backup_") {
			item.full = true
			item.date = backupDate(name, options.ProjectName, info.ModTime())
		} else if name == options.ProjectName+"-daily.tar.gz" || name == options.ProjectName+"-daily.tar.gz.enc" ||
			name == options.ProjectName+"-hourly.tar.gz" || name == options.ProjectName+"-hourly.tar.gz.enc" {
			item.date = info.ModTime().UTC()
		} else {
			item.full = true
			item.date = backupDate(name, options.ProjectName, info.ModTime())
		}
		files = append(files, item)
	}
	protectedBase := make(map[string]bool)
	for _, item := range files {
		if item.name != options.ProjectName+"-daily.tar.gz" && item.name != options.ProjectName+"-daily.tar.gz.enc" &&
			item.name != options.ProjectName+"-hourly.tar.gz" && item.name != options.ProjectName+"-hourly.tar.gz.enc" {
			continue
		}
		manifest, _, _, err := readArchive(ctx, item.path, options.Password, DefaultLimits())
		if err != nil {
			return nil, fmt.Errorf("cannot safely prune without verifying incremental %q: %w", item.name, err)
		}
		if manifest.Type == "incremental" && manifest.BaseBackup != "" {
			protectedBase[manifest.BaseBackup] = true
		}
	}
	fulls := make([]int, 0)
	for index := range files {
		if files[index].full {
			fulls = append(fulls, index)
		}
	}
	sort.Slice(fulls, func(i, j int) bool { return files[fulls[i]].date.After(files[fulls[j]].date) })
	keepSpecial(files, fulls, func(file backupFile) bool { return file.date.Month() == time.January && file.date.Day() == 1 }, options.KeepYearly)
	keepSpecial(files, fulls, func(file backupFile) bool { return file.date.Day() == 1 }, options.KeepMonthly)
	keepSpecial(files, fulls, func(file backupFile) bool { return file.date.Weekday() == time.Sunday }, options.KeepWeekly)
	dailyKept := 0
	for _, index := range fulls {
		if files[index].keep {
			continue
		}
		if dailyKept < options.MaxBackups {
			files[index].keep = true
			dailyKept++
		}
	}
	for index := range files {
		if files[index].name == options.ProjectName+"-daily.tar.gz" || files[index].name == options.ProjectName+"-daily.tar.gz.enc" ||
			files[index].name == options.ProjectName+"-hourly.tar.gz" || files[index].name == options.ProjectName+"-hourly.tar.gz.enc" {
			files[index].keep = true
		}
		if protectedBase[files[index].name] {
			files[index].keep = true
		}
	}
	var deleted []string
	for index := range files {
		if files[index].keep {
			continue
		}
		if err := deleteBackup(files[index], "retention policy", options.OnDelete); err != nil {
			return deleted, err
		}
		deleted = append(deleted, files[index].name)
	}
	if maxTotal != 0 {
		capBytes := maxTotal
		if maxTotal < 0 {
			usage, err := disk.Usage(options.BackupDir)
			if err != nil {
				return deleted, fmt.Errorf("resolve backup volume size cap: %w", err)
			}
			capBytes = int64(usage.Total) * (-maxTotal) / 100
		}
		var total int64
		for _, file := range files {
			if exists, _ := os.Stat(file.path); exists != nil {
				total += exists.Size()
			}
		}
		sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })
		for index := range files {
			if total <= capBytes {
				break
			}
			info, err := os.Stat(files[index].path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return deleted, err
			}
			if protectedBase[files[index].name] {
				continue
			}
			if err := deleteBackup(files[index], "maximum total size", options.OnDelete); err != nil {
				return deleted, err
			}
			deleted = append(deleted, files[index].name)
			total -= info.Size()
			if files[index].full {
				delete(protectedBase, files[index].name)
			}
		}
		if total > capBytes {
			return deleted, fmt.Errorf("backup size cap is %d bytes but %d bytes remain in protected backups", capBytes, total)
		}
	}
	return deleted, nil
}

func keepSpecial(files []backupFile, fulls []int, match func(backupFile) bool, count int) {
	if count <= 0 {
		return
	}
	kept := 0
	for _, index := range fulls {
		if !match(files[index]) {
			continue
		}
		files[index].keep = true
		kept++
		if kept == count {
			return
		}
	}
}

func backupDate(name, project string, fallback time.Time) time.Time {
	value := strings.TrimPrefix(name, project+"_backup_")
	value = strings.TrimSuffix(strings.TrimSuffix(value, ".enc"), ".tar.gz")
	for _, layout := range []string{"2006-01-02_150405", "2006-01-02_1504", "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return parsed
		}
	}
	return fallback.UTC()
}

func deleteBackup(file backupFile, reason string, callback func(string, string) error) error {
	if err := os.Remove(file.path); err != nil {
		return fmt.Errorf("delete backup %q: %w", file.name, err)
	}
	if callback != nil {
		if err := callback(file.name, reason); err != nil {
			return fmt.Errorf("record backup deletion audit event after deleting %q: %w", file.name, err)
		}
	}
	return nil
}

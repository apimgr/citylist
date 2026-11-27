package health

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

type CheckFunc func() (string, error)

type Result struct {
	Key      string
	Status   string
	Critical bool
}

type entry struct {
	key      string
	critical bool
	check    CheckFunc
	status   string
}

var (
	registryMu sync.RWMutex
	checks     = map[string]*entry{}
	checkOrder []string
	checkKey   = regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)*$`)
)

// Register adds a public-safe dataset or dependency check. Checks run only
// when Refresh is called; health endpoints read their cached status.
func Register(key string, critical bool, check CheckFunc) {
	key = strings.TrimSpace(key)
	if !checkKey.MatchString(key) || check == nil {
		return
	}
	status, err := checkedStatus(check)
	if err != nil {
		status = "error"
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := checks[key]; !exists {
		checkOrder = append(checkOrder, key)
	}
	checks[key] = &entry{key: key, critical: critical, check: check, status: status}
}

// Refresh rechecks registered dependencies and stores only public status values.
func Refresh() error {
	registryMu.RLock()
	entries := make([]*entry, 0, len(checkOrder))
	for _, key := range checkOrder {
		if current := checks[key]; current != nil {
			copy := *current
			entries = append(entries, &copy)
		}
	}
	registryMu.RUnlock()

	for _, current := range entries {
		status, err := checkedStatus(current.check)
		if err != nil {
			status = "error"
		}
		registryMu.Lock()
		if registered := checks[current.key]; registered != nil {
			registered.status = status
		}
		registryMu.Unlock()
	}
	return nil
}

func checkedStatus(check CheckFunc) (string, error) {
	status, err := check()
	if err != nil {
		return "error", err
	}
	switch status {
	case "ok", "degraded", "error":
		return status, nil
	default:
		return "error", nil
	}
}

// Results returns cached checks in their registration order.
func Results() []Result {
	registryMu.RLock()
	defer registryMu.RUnlock()
	results := make([]Result, 0, len(checkOrder))
	for _, key := range checkOrder {
		if current := checks[key]; current != nil {
			results = append(results, Result{Key: key, Status: current.status, Critical: current.critical})
		}
	}
	return results
}

// RegisteredKeys returns a sorted copy for diagnostics and tests.
func RegisteredKeys() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	keys := append([]string(nil), checkOrder...)
	sort.Strings(keys)
	return keys
}

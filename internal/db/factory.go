package db

import (
	"fmt"
	"strings"
	"sync"
)

// DriverFactory is a function that creates an Adapter from a DSN.
type DriverFactory func(dsn string) (Adapter, error)

var (
	driversMu sync.RWMutex
	drivers   = map[string]DriverFactory{}
)

// RegisterDriver registers a driver factory for a given scheme (e.g. "mysql").
// This is typically called from an init() function in the driver package.
func RegisterDriver(scheme string, factory DriverFactory) {
	driversMu.Lock()
	defer driversMu.Unlock()
	drivers[scheme] = factory
}

// NewAdapter creates an Adapter from a DSN string.
// The scheme prefix (e.g. "mysql://") determines which driver is used.
// Drivers must be registered via RegisterDriver (usually via blank imports).
func NewAdapter(dsn string) (Adapter, error) {
	scheme := dsnScheme(dsn)
	if scheme == "" {
		return nil, fmt.Errorf("unsupported database scheme in DSN %q (supported: mysql://)", dsn)
	}
	driversMu.RLock()
	factory, ok := drivers[scheme]
	driversMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unsupported database scheme in DSN %q (supported: mysql://)", dsn)
	}
	return factory(dsn)
}

func dsnScheme(dsn string) string {
	if idx := strings.Index(dsn, "://"); idx > 0 {
		return dsn[:idx]
	}
	return ""
}

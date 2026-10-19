package config

import (
	"fmt"
	"strconv"
	"strings"
)

// Retention and size limits an operator may change. Both are read-only
// configuration: set in the environment, never from the API, so a compromised
// administrator session cannot shorten the audit history or raise the memory a
// hostile service can make Wheelhouse read.
const (
	// EnvAuditRetentionDays names the variable that sets how long audit rows are kept.
	EnvAuditRetentionDays = "WHEELHOUSE_AUDIT_RETENTION_DAYS"
	// DefaultAuditRetentionDays is how long audit rows are kept by default.
	DefaultAuditRetentionDays = 180
	// MinAuditRetentionDays is the shortest retention. It equals the database's
	// hard floor (store.AuditFloorDays; a test keeps them equal): rows younger
	// than that cannot be deleted whatever is configured.
	MinAuditRetentionDays = 30
	// MaxAuditRetentionDays is the longest retention (ten years).
	MaxAuditRetentionDays = 3650

	// EnvMaxLibraryResponseMiB names the variable that sets the cap on the list a
	// media manager returns.
	EnvMaxLibraryResponseMiB = "WHEELHOUSE_MAX_LIBRARY_RESPONSE_MIB"
	// DefaultMaxLibraryResponseMiB is the default cap on a Radarr or Sonarr list.
	DefaultMaxLibraryResponseMiB = 96
	// MinMaxLibraryResponseMiB and MaxMaxLibraryResponseMiB bound the setting.
	MinMaxLibraryResponseMiB = 16
	MaxMaxLibraryResponseMiB = 512
)

// AuditRetentionDays parses WHEELHOUSE_AUDIT_RETENTION_DAYS: whole days from 30
// to 3650, default 180. An empty variable counts as unset; anything else that
// is not in range is an error, so a typo cannot silently keep or drop history.
func AuditRetentionDays(getenv func(string) string) (int, error) {
	return boundedInt(getenv, EnvAuditRetentionDays, DefaultAuditRetentionDays, MinAuditRetentionDays, MaxAuditRetentionDays, "days")
}

// MaxLibraryResponseMiB parses WHEELHOUSE_MAX_LIBRARY_RESPONSE_MIB: whole MiB
// from 16 to 512, default 96.
func MaxLibraryResponseMiB(getenv func(string) string) (int, error) {
	return boundedInt(getenv, EnvMaxLibraryResponseMiB, DefaultMaxLibraryResponseMiB, MinMaxLibraryResponseMiB, MaxMaxLibraryResponseMiB, "MiB")
}

func boundedInt(getenv func(string) string, name string, def, lo, hi int, unit string) (int, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < lo || n > hi {
		return 0, fmt.Errorf("%s must be a whole number of %s from %d to %d (default %d)", name, unit, lo, hi, def)
	}
	return n, nil
}

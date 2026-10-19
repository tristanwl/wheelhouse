package config

import (
	"strings"
	"testing"
)

func oneVar(name, value string) func(string) string {
	return func(k string) string {
		if k == name {
			return value
		}
		return ""
	}
}

func TestAuditRetentionDays(t *testing.T) {
	for in, want := range map[string]int{"": 180, "180": 180, "30": 30, "3650": 3650, " 365 ": 365} {
		got, err := AuditRetentionDays(oneVar(EnvAuditRetentionDays, in))
		if err != nil || got != want {
			t.Errorf("%q: %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"29", "0", "-1", "3651", "abc", "1.5", "180d", "1e2", "99999999999999999999"} {
		_, err := AuditRetentionDays(oneVar(EnvAuditRetentionDays, in))
		if err == nil || !strings.Contains(err.Error(), "WHEELHOUSE_AUDIT_RETENTION_DAYS") || !strings.Contains(err.Error(), "30 to 3650") {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestMaxLibraryResponseMiB(t *testing.T) {
	for in, want := range map[string]int{"": 96, "96": 96, "16": 16, "512": 512} {
		got, err := MaxLibraryResponseMiB(oneVar(EnvMaxLibraryResponseMiB, in))
		if err != nil || got != want {
			t.Errorf("%q: %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"15", "513", "0", "-96", "lots", "96MiB", "1.5"} {
		_, err := MaxLibraryResponseMiB(oneVar(EnvMaxLibraryResponseMiB, in))
		if err == nil || !strings.Contains(err.Error(), "WHEELHOUSE_MAX_LIBRARY_RESPONSE_MIB") {
			t.Errorf("%q: %v", in, err)
		}
	}
}

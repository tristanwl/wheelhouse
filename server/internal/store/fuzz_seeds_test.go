package store

import (
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/fuzzseed"
)

// TestWriteFuzzSeeds regenerates testdata/fuzz from the real migration file
// names, the bad sets of TestLoadMigrationsRejectsBadSets and hostile paths:
//
//	WHEELHOUSE_WRITE_FUZZ_SEEDS=1 go test -run TestWriteFuzzSeeds ./internal/store
func TestWriteFuzzSeeds(t *testing.T) {
	w := fuzzseed.New(t)
	for _, c := range [][3]string{
		{"0001_meta.sql", "0002_auth.sql", "0003_instances.sql"}, {"0001_a.sql", "0003_c.sql", ""}, {"0002_a.sql", "", ""},
		{"0001_a.sql", "0001_b.sql", ""}, {"1_a.sql", "", ""}, {"0001_A.sql", "", ""}, {"0001_a.txt", "", ""}, {"0001.sql", "", ""},
		{"0001_a.sql", "0002_b.sql", "0003_c.sql"}, {"0001_a.sql", "0002_b.sql", "0002_b.sql"}, {"0001_a b.sql", "", ""},
		{"0001_a.sql.sql", "", ""}, {"٠٠٠١_a.sql", "", ""}, {"0001_ä.sql", "", ""}, {"0001_a.sql\n", "", ""}, {"0001_.sql", "", ""},
		{"0010_a.sql", "0009_a.sql", "0011_a.sql"}, {"00001_a.sql", "", ""}, {"..", "0001_a.sql", ""}, {"0001_a.sql/x", "", ""},
	} {
		w.Add("FuzzLoadMigrations", "set", c[0], c[1], c[2], "select 1;", false)
	}
	w.Add("FuzzLoadMigrations", "directory", "0001_a.sql", "0002_b.sql", "", "select 1;", true)
	for _, p := range []string{
		"/config/wheelhouse.db", "/tmp/a b/c.db", "/tmp/a?b=c#d.db", "/tmp/a%20b.db", "/tmp/a&_pragma=query_only(0).db",
		"/tmp/x?_pragma=journal_mode(OFF)", "/tmp/ü/é.db", "/tmp/new\nline.db", "/tmp/nul\x00.db", "/", "//host/share/x.db",
		"/tmp/file:x.db", "/tmp/?mode=ro", "/tmp/#fragment", "/tmp/%00", "/tmp/%zz", "/tmp/a+b.db", "/tmp/;x.db",
	} {
		w.Add("FuzzDSN", "path", p, true)
		w.Add("FuzzDSN", "path-reader", p, false)
	}
}

package downloadclient

import (
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/fuzzseed"
)

// TestWriteFuzzSeeds regenerates testdata/fuzz from the number forms SABnzbd and
// qBittorrent really send and the hostile ones around them:
//
//	WHEELHOUSE_WRITE_FUZZ_SEEDS=1 go test -run TestWriteFuzzSeeds ./internal/providers/downloadclient
func TestWriteFuzzSeeds(t *testing.T) {
	w := fuzzseed.New(t)
	for _, v := range []string{
		`51200`, `"51200.00"`, `"51200"`, `""`, `" 12 "`, `null`, `true`, `[]`, `{}`, `-1`, `"-1"`, `1e999`, `-1e999`, `"1e999"`, `"NaN"`,
		`"Infinity"`, `"-Infinity"`, `"0x10"`, `"1_000"`, `"1,5"`, `"١٢٣"`, `9007199254740993`, `1.7976931348623157e308`, `5e-324`,
		`"9007199254740993"`, `0.1`, `-0`, `"-0"`, `1E5`, `"+5"`, `"5 GB"`, ``, `n`, `"`, `"\u0000"`, `1.0000000000000002`, `"4.9e-324"`,
	} {
		w.Add("FuzzNumber", "value", []byte(v), 1.0)
		w.Add("FuzzNumber", "value-kib", []byte(v), 1024.0)
	}
	for _, k := range []float64{0, -1, 1e300, 1e-300, 1024 * 1024 * 1024} {
		w.Add("FuzzNumber", "factor", []byte(`"12.5"`), k)
		w.Add("FuzzNumber", "factor-big", []byte(`1e308`), k)
	}
}

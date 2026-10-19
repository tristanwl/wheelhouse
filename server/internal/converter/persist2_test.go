package converter

import (
	"os"
	"strings"
	"testing"
)

// A request that bypassed the route's validation (a bug, or a stale row loaded
// by some future code path) still never reaches ffmpeg with an unvalidated
// argument: the queue checks the enums again before it runs a job.
func TestJobWithInvalidEnumsNeverReachesFfmpeg(t *testing.T) {
	root, file := library(t, "a.mkv", "x")
	ft := newFakeTools(t, "ok", probeTwoSeconds)
	q := newQueue(t, root, ft, nil)
	req := request(file)
	req.Backend = "libx265 -i /etc/passwd"
	j := wait(t, q, q.Enqueue(req).ID, StatusFailed)
	if !strings.Contains(j.Error, "not valid") {
		t.Errorf("%+v", j)
	}
	if _, err := os.Stat(ft.argsFile); err == nil {
		t.Error("ffmpeg ran with an unvalidated backend")
	}
}

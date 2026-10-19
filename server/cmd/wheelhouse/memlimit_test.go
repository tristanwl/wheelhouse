package main

import (
	"errors"
	"testing"
)

func fakeFiles(files map[string]string) func(string) ([]byte, error) {
	return func(name string) ([]byte, error) {
		if v, ok := files[name]; ok {
			return []byte(v), nil
		}
		return nil, errors.New("not found")
	}
}

func TestMemoryLimitFollowsTheContainerLimit(t *testing.T) {
	const gib = int64(1) << 30
	cases := []struct {
		name  string
		env   string
		files map[string]string
		want  int64
	}{
		{"cgroup v2 limit", "", map[string]string{"/sys/fs/cgroup/memory.max": "1073741824\n"}, gib / 100 * 80},
		{"cgroup v1 limit", "", map[string]string{"/sys/fs/cgroup/memory/memory.limit_in_bytes": "2147483648"}, 2 * gib / 100 * 80},
		{"unlimited v2", "", map[string]string{"/sys/fs/cgroup/memory.max": "max\n"}, 0},
		{"unlimited v1 (huge number)", "", map[string]string{"/sys/fs/cgroup/memory/memory.limit_in_bytes": "9223372036854771712"}, 0},
		{"no cgroup", "", nil, 0},
		{"garbage", "", map[string]string{"/sys/fs/cgroup/memory.max": "lots"}, 0},
		{"zero", "", map[string]string{"/sys/fs/cgroup/memory.max": "0"}, 0},
		{"the operator's GOMEMLIMIT wins", "512MiB", map[string]string{"/sys/fs/cgroup/memory.max": "1073741824"}, 0},
	}
	for _, c := range cases {
		var set int64
		got := applyMemoryLimit(func(k string) string {
			if k == "GOMEMLIMIT" {
				return c.env
			}
			return ""
		}, fakeFiles(c.files), func(n int64) int64 { set = n; return 0 })
		if got != c.want || set != c.want {
			t.Errorf("%s: returned %d, set %d, want %d", c.name, got, set, c.want)
		}
	}
}

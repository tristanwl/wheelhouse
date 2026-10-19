package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/tristanwl/wheelhouse/server/internal/fuzzseed"
)

// TestWriteFuzzSeeds regenerates testdata/fuzz from the cases of the auth tests,
// the common-password list and the hostile spellings the threat model names:
//
//	WHEELHOUSE_WRITE_FUZZ_SEEDS=1 go test -run TestWriteFuzzSeeds ./internal/auth
func TestWriteFuzzSeeds(t *testing.T) {
	w := fuzzseed.New(t)

	for _, name := range []string{
		"admin-user", "Alice", "a.b_c@d-e", "abc", "ab", "", strings.Repeat("a", 64), strings.Repeat("a", 65),
		"-abc", ".abc", "_abc", "a bc", "abc ", "abc\n", "abc\x00", "ab​c", "ａｄｍｉｎ", "admın", "ADMIN", "Kelvin",
		"local-network", "LOCAL-NETWORK", "Cli", "SYSTEM", "anonymous", "anonymous\n", "système", "a@b", "../etc",
	} {
		w.Add("FuzzValidateUsername", "name", name)
	}

	raw := commonPasswordsRaw
	var common []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			common = append(common, line)
		}
	}
	for _, i := range []int{0, 1, 2, 50, 400, 1000, 2000} {
		w.Add("FuzzValidatePassword", "common", strings.ToUpper(common[i]), "admin-user")
		w.Add("FuzzValidatePassword", "common-lower", common[i], "x")
	}
	for _, c := range [][2]string{
		{goodPW, "admin-user"}, {otherPW, "admin-user"}, {"short", "admin-user"}, {"Admin-User-123", "admin-user"},
		{"ADMIN-USER-123", "admin-user-123"}, {"aaaaaaaaaaaaaaaa", "x"}, {"abababababababab", "x"}, {"abcdabcdabcdabcd", "x"},
		{strings.Repeat("😀", 11), "x"}, {strings.Repeat("😀", 12), "x"}, {"😀😁😂🤣😃😄😅😆😉😊😋😎", "x"}, {"日本語のパスワードです長い", "x"},
		{"ééééééé", "x"}, {"good\x00password-123", "x"}, {"bad\xffutf8-password-123", "x"},
		{strings.Repeat("x1", 512), "x"}, {strings.Repeat("x1", 513), "x"}, {"correct horse battery staple", "x"},
		{"　　　　　　　　　　　　", "x"}, {"            ", "x"},
	} {
		w.Add("FuzzValidatePassword", "case", c[0], c[1])
	}

	// A real hash (the parameters the tests use) and the hostile variants of it.
	h, err := newHasher(cheap).Hash(context.Background(), goodPW)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(h, "$")
	for _, s := range []string{
		h,
		"$argon2id$v=19$m=65536,t=3,p=2$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=4294967295,t=3,p=2$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=1048576,t=20,p=16$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=1048577,t=3,p=2$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=8,t=0,p=1$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=8,t=1,p=0$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=8,t=1,p=256$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=8,m=8,t=1,p=1$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=8,t=1$" + parts[4] + "$" + parts[5],
		"$argon2id$v=16$m=8,t=1,p=1$" + parts[4] + "$" + parts[5],
		"$argon2i$v=19$m=8,t=1,p=1$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=8,t=1,p=1$$" + parts[5],
		"$argon2id$v=19$m=8,t=1,p=1$" + parts[4] + "$",
		"$argon2id$v=19$m=8,t=1,p=1$" + parts[4] + "$" + parts[5] + "$extra",
		"$argon2id$v=19$m=8,t=1,p=1$!!!!$" + parts[5],
		"$argon2id$v=19$m=-8,t=1,p=1$" + parts[4] + "$" + parts[5],
		"", "$", "$$$$$$", "plain-text-password", strings.Repeat("$", 1000),
	} {
		w.Add("FuzzParseHash", "hash", s)
	}

	for _, c := range []struct {
		v string
		n uint8
	}{
		{"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36", 200}, {"line\r\nbreak\tand\x00nul\x7f", 64}, {"ünïcödé-ünïcödé-ünïcödé", 7},
		{"😀😀😀😀", 5}, {"\xff\xfe\xfdinvalid", 8}, {"", 0}, {strings.Repeat("a", 300), 200}, {"‮RTL override", 64}, {"a b", 64},
	} {
		w.Add("FuzzCleanClip", "text", c.v, c.n)
	}

	for _, tok := range []string{
		"", strings.Repeat("A", 43), strings.Repeat("a", 42), strings.Repeat("a", 44), strings.Repeat("-", 43), strings.Repeat("_", 43),
		strings.Repeat("=", 43), strings.Repeat("a", 42) + "\x00", strings.Repeat("a", 42) + "'", "' OR '1'='1", strings.Repeat("a", 43) + "\n",
		"a%00b", strings.Repeat("é", 22), "Ab3_-Ab3_-Ab3_-Ab3_-Ab3_-Ab3_-Ab3_-Ab3_-Ab3_-Ab",
	} {
		w.Add("FuzzAuthenticate", "token", tok)
	}

	for _, c := range [][4]string{
		{"fuzz-admin", "correct-battery-staple-1", "192.0.2.1", "Mozilla/5.0"},
		{"FUZZ-ADMIN", "correct-battery-staple-1", "2001:db8::1", ""},
		{"fuzz-admin", "wrong", "192.0.2.1", "ua"},
		{" fuzz-admin", "correct-battery-staple-1", "192.0.2.1", "ua"},
		{"fuzz-admin\x00", "correct-battery-staple-1", "192.0.2.1", "ua"},
		{"fuzz-admin' --", "x", "192.0.2.1", "ua"},
		{"fuzz-admin", "correct-battery-staple-1" + strings.Repeat("x", 1100), "192.0.2.1", "ua"},
		{strings.Repeat("a", 300), "x", "192.0.2.1", "ua"},
		{"fuzz-admin", "correct-battery-staple-1", "\r\nX-Injected: 1", "line\r\nbreak\x00\x7f"},
		{"fuzz-admin", "correct-battery-staple-1", strings.Repeat("9", 200), strings.Repeat("ü", 300)},
		{"FUZZ-ADMİN", "correct-battery-staple-1", "192.0.2.1", "ua"},
		{"", "", "", ""},
	} {
		w.Add("FuzzLogin", "creds", c[0], c[1], c[2], c[3])
	}
}

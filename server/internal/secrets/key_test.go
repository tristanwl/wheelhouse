package secrets

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func sprintf(format string, a ...any) string { return fmt.Sprintf(format, a...) }

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func writeKey(t *testing.T, path string, b []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, b, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil { // WriteFile is subject to the umask
		t.Fatal(err)
	}
}

func rawKey(t *testing.T) []byte {
	t.Helper()
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Clone(k.b)
}

func TestLoadOrder(t *testing.T) {
	dir := t.TempDir()
	fileKey, envKey, genKey := rawKey(t), rawKey(t), rawKey(t)
	kf := filepath.Join(dir, "operator.key")
	writeKey(t, kf, fileKey, 0o600)
	writeKey(t, filepath.Join(dir, GeneratedKeyName), genKey, 0o600)
	envB64 := base64.StdEncoding.EncodeToString(envKey)

	// 1. the key file wins over everything.
	l, err := Load(LoadOptions{Getenv: env(map[string]string{EnvKeyFile: kf, EnvKey: envB64}), ConfigDir: dir})
	if err != nil || l.Source != SourceKeyFile || !bytes.Equal(l.Key.b, fileKey) || l.Path != kf {
		t.Fatalf("key file: %+v, %v", l, err)
	}
	// 2. then the environment variable (base64, whitespace tolerated).
	l, err = Load(LoadOptions{Getenv: env(map[string]string{EnvKey: " " + envB64 + "\n"}), ConfigDir: dir})
	if err != nil || l.Source != SourceEnv || !bytes.Equal(l.Key.b, envKey) {
		t.Fatalf("env: %+v, %v", l, err)
	}
	// 3. then the generated file in the config directory.
	l, err = Load(LoadOptions{Getenv: env(nil), ConfigDir: dir})
	if err != nil || l.Source != SourceGenerated || !bytes.Equal(l.Key.b, genKey) {
		t.Fatalf("generated: %+v, %v", l, err)
	}
}

func TestLoadSetButBrokenSourceDoesNotFallThrough(t *testing.T) {
	dir := t.TempDir()
	writeKey(t, filepath.Join(dir, GeneratedKeyName), rawKey(t), 0o600)
	good := base64.StdEncoding.EncodeToString(rawKey(t))
	for name, e := range map[string]map[string]string{
		"missing key file": {EnvKeyFile: filepath.Join(dir, "nope.key"), EnvKey: good},
		"bad base64":       {EnvKey: "not base64!!"},
		"short base64":     {EnvKey: base64.StdEncoding.EncodeToString(make([]byte, 16))},
		"zero key":         {EnvKey: base64.StdEncoding.EncodeToString(make([]byte, 32))},
		"url alphabet":     {EnvKey: strings.NewReplacer("+", "-", "/", "_").Replace(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xfb, 0xff}, 16)))},
	} {
		if _, err := Load(LoadOptions{Getenv: env(e), ConfigDir: dir}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLoadGeneratesOnlyWhenAllowed(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(LoadOptions{Getenv: env(nil), ConfigDir: dir}); !errors.Is(err, ErrNoKey) {
		t.Fatalf("without Generate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, GeneratedKeyName)); err == nil {
		t.Fatal("a key was created without Generate")
	}
	l, err := Load(LoadOptions{Getenv: env(nil), ConfigDir: dir, Generate: true})
	if err != nil || l.Source != SourceGenerated {
		t.Fatal(l, err)
	}
	st, err := os.Stat(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("generated key has mode %v", st.Mode().Perm())
	}
	if st.Size() != KeySize {
		t.Errorf("generated key is %d bytes", st.Size())
	}
	// A second start reads the same key, it does not make a new one.
	l2, err := Load(LoadOptions{Getenv: env(nil), ConfigDir: dir, Generate: true})
	if err != nil || !l2.Key.Equal(l.Key) {
		t.Fatalf("second load: %v", err)
	}
	// No config directory and nothing set.
	if _, err := Load(LoadOptions{Getenv: env(nil)}); !errors.Is(err, ErrNoKey) {
		t.Errorf("no dir: %v", err)
	}
	// A missing config directory is an error, not a created directory.
	if _, err := Load(LoadOptions{Getenv: env(nil), ConfigDir: filepath.Join(dir, "absent"), Generate: true}); err == nil {
		t.Error("generated a key in a directory that does not exist")
	}
}

func TestGenerateKeyFileNeverOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k")
	existing := rawKey(t)
	writeKey(t, path, existing, 0o600)
	if _, err := GenerateKeyFile(path); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v", err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, existing) {
		t.Error("an existing key was overwritten")
	}
}

func TestKeyFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful")
	}
	dir := t.TempDir()
	for _, mode := range []os.FileMode{0o600, 0o400} {
		p := filepath.Join(dir, "ok")
		_ = os.Remove(p)
		writeKey(t, p, rawKey(t), mode)
		if _, err := ReadKeyFile(p); err != nil {
			t.Errorf("mode %v refused: %v", mode, err)
		}
	}
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o660, 0o666, 0o601, 0o610, 0o444, 0o440, 0o404} {
		p := filepath.Join(dir, "bad")
		_ = os.Remove(p)
		writeKey(t, p, rawKey(t), mode)
		_, err := ReadKeyFile(p)
		if err == nil {
			t.Errorf("mode %04o accepted", mode)
			continue
		}
		if !strings.Contains(err.Error(), "chmod 600") || !strings.Contains(err.Error(), "too open") {
			t.Errorf("mode %04o: unhelpful error %q", mode, err)
		}
	}
	// The same check applies through Load for each file source.
	p := filepath.Join(dir, "open.key")
	writeKey(t, p, rawKey(t), 0o644)
	if _, err := Load(LoadOptions{Getenv: env(map[string]string{EnvKeyFile: p})}); err == nil {
		t.Error("Load accepted an open key file")
	}
	writeKey(t, filepath.Join(dir, GeneratedKeyName), rawKey(t), 0o640)
	if _, err := Load(LoadOptions{Getenv: env(nil), ConfigDir: dir, Generate: true}); err == nil {
		t.Error("Load accepted an open generated key and must not replace it")
	}
}

func TestKeyFileSize(t *testing.T) {
	dir := t.TempDir()
	good := rawKey(t)
	for name, b := range map[string][]byte{
		"empty":           {},
		"31":              good[:31],
		"33":              append(bytes.Clone(good), 0),
		"trailing nl":     append(bytes.Clone(good), '\n'),
		"hex text":        []byte(fmt.Sprintf("%x", good)),
		"huge":            bytes.Repeat([]byte{1}, 1<<20),
		"zeros":           make([]byte, 32),
		"base64 text key": []byte(base64.StdEncoding.EncodeToString(good)),
	} {
		p := filepath.Join(dir, "k")
		_ = os.Remove(p)
		writeKey(t, p, b, 0o600)
		_, err := ReadKeyFile(p)
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if name != "zeros" && !strings.Contains(err.Error(), "exactly 32 raw bytes") {
			t.Errorf("%s: error does not say what to do: %v", name, err)
		}
	}
}

func TestKeyFileMustBeRegular(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadKeyFile(dir); err == nil {
		t.Error("a directory was accepted")
	}
	// A symlink (secret mounts use them) is followed and the target checked.
	target := filepath.Join(dir, "target")
	writeKey(t, target, rawKey(t), 0o600)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("no symlinks")
	}
	if _, err := ReadKeyFile(link); err != nil {
		t.Errorf("symlink to a good key: %v", err)
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(target, 0o644)
		if _, err := ReadKeyFile(link); err == nil {
			t.Error("symlink to an open file was accepted")
		}
	}
}

func TestPromoteKeyFile(t *testing.T) {
	dir := t.TempDir()
	live, next := filepath.Join(dir, "secret.key"), filepath.Join(dir, "secret.key.next")
	oldK, newK := rawKey(t), rawKey(t)
	writeKey(t, live, oldK, 0o600)
	writeKey(t, next, newK, 0o600)
	if err := PromoteKeyFile(next, live); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(live)
	if !bytes.Equal(got, newK) {
		t.Error("not promoted")
	}
	if _, err := os.Stat(next); err == nil {
		t.Error("staged file left behind")
	}
}

func TestKeyNeverPrints(t *testing.T) {
	k, _ := GenerateKey()
	hexKey := fmt.Sprintf("%x", k.b)
	b64 := base64.StdEncoding.EncodeToString(k.b)

	var logs bytes.Buffer
	slog.New(slog.NewTextHandler(&logs, nil)).Info("loaded", "key", k, "loaded", Loaded{Key: k, Source: SourceEnv}, "secret", NewSecret("canary-plaintext"))
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("loaded", "key", k, "secret", NewSecret("canary-plaintext"))
	js, err := json.Marshal(map[string]any{"k": k, "l": Loaded{Key: k}, "s": NewSecret("canary-plaintext")})
	if err != nil {
		t.Fatal(err)
	}
	all := logs.String() + string(js) + sprintf("%v %+v %#v %x %s", k, Loaded{Key: k}, Loaded{Key: k}, k, k)
	for _, bad := range []string{hexKey, b64, string(k.b), "canary-plaintext"} {
		if strings.Contains(all, bad) {
			t.Errorf("output contains %q", bad[:min(len(bad), 8)])
		}
	}
	if !strings.Contains(all, "[redacted]") {
		t.Error("expected the redaction marker")
	}
	// Errors from loading a bad key never include its bytes either.
	p := filepath.Join(t.TempDir(), "k")
	writeKey(t, p, append(bytes.Clone(k.b), 'x'), 0o600)
	_, err = ReadKeyFile(p)
	if err == nil || strings.Contains(err.Error(), hexKey) || strings.Contains(err.Error(), string(k.b)) {
		t.Errorf("error leaked: %v", err)
	}
}

func TestSourceString(t *testing.T) {
	for _, s := range []Source{SourceKeyFile, SourceEnv, SourceGenerated, 0} {
		if s.String() == "" {
			t.Errorf("%d has no name", s)
		}
	}
}

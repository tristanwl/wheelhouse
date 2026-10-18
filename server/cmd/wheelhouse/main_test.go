package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestHealthcheck(t *testing.T) {
	var path string
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(status)
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	// PORT is honoured, and a HOSTNAME set by the container runtime is not.
	env := envOf(map[string]string{"PORT": port, "HOSTNAME": "4f2a9c1d7e3b"})
	if err := healthcheck(env, http.DefaultTransport); err != nil {
		t.Fatalf("healthy server: %v", err)
	}
	if path != "/healthz" {
		t.Errorf("asked for %q", path)
	}

	status = http.StatusInternalServerError
	if err := healthcheck(env, http.DefaultTransport); err == nil {
		t.Error("a 500 must be unhealthy")
	}

	srv.Close()
	if err := healthcheck(env, http.DefaultTransport); err == nil {
		t.Error("a closed port must be unhealthy")
	}
	if err := healthcheck(envOf(map[string]string{"PORT": "nope"}), http.DefaultTransport); err == nil {
		t.Error("a bad PORT must be an error")
	}
}

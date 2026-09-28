package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthcheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ok.Close()
	_, port, _ := net.SplitHostPort(ok.Listener.Addr().String())

	if code := healthcheck(":" + port); code != 0 {
		t.Errorf("healthy server: exit %d, want 0", code)
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer down.Close()
	if code := healthcheck(down.Listener.Addr().String()); code != 1 {
		t.Errorf("503: exit %d, want 1", code)
	}

	closed := down.Listener.Addr().String()
	down.Close()
	if code := healthcheck(closed); code != 1 {
		t.Errorf("nothing listening: exit %d, want 1", code)
	}

	if code := healthcheck("no-port"); code != 1 {
		t.Errorf("bad addr: exit %d, want 1", code)
	}
}

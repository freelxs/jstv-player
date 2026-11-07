package catvod

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProtocolErrorsSeparateDiagnosticsFromUserMessage(t *testing.T) {
	for _, tc := range []struct {
		status     int
		body, kind string
	}{
		{500, `{"error":"private fixture token=secret"}`, "http"},
		{200, `<html>private fixture</html>`, "json"},
		{200, `{"error":"private fixture token=secret"}`, "business"},
		{200, `{"code":-1,"message":"private fixture token=secret"}`, "business"},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); w.Write([]byte(tc.body)) }))
		var log string
		client := &Client{Base: server.URL, LogFunc: func(s string) { log = s }}
		_, err := client.Home("/test")
		server.Close()
		var protocol *ProtocolError
		if !errors.As(err, &protocol) || protocol.Kind != tc.kind {
			t.Fatalf("expected %s error: %v", tc.kind, err)
		}
		if strings.Contains(err.Error(), "private fixture") || strings.Contains(err.Error(), "secret") {
			t.Fatalf("raw details in UI: %v", err)
		}
		if !strings.Contains(log, "private fixture") {
			t.Fatal("diagnostics missing")
		}
	}
}

func TestProtocolResponseLimitAppliesToSuccessAndErrorBodies(t *testing.T) {
	for _, status := range []int{200, 500} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			chunk := strings.Repeat("x", 1<<20)
			for i := 0; i < 33; i++ {
				if _, err := w.Write([]byte(chunk)); err != nil {
					return
				}
			}
		}))
		_, err := (&Client{Base: server.URL}).Config()
		server.Close()
		var protocol *ProtocolError
		if !errors.As(err, &protocol) || protocol.Kind != "size" || !errors.Is(err, errTooLarge) {
			t.Fatalf("unbounded or truncated response: %v", err)
		}
	}
}

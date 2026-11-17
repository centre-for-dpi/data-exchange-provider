package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type seenCall struct {
	header, method, uri string
	body                []byte
}

func assertionHandler(t *testing.T, result string, require bool) (http.Handler, *bytes.Buffer, *seenCall, *[]byte) {
	t.Helper()
	logs := &bytes.Buffer{}
	seen := &seenCall{}
	got := &[]byte{}
	h := NewHandlerWith(nodeName, []Route{{Path: "/salud", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte("ok"))
	})}}, slog.New(slog.NewJSONHandler(logs, nil)), Options{
		Assertion: func(_ context.Context, header, method, uri string, body []byte) string {
			*seen = seenCall{header, method, uri, body}
			return result
		},
		RequireAssertion: require,
	})
	return h, logs, seen, got
}

// verify: the result is logged (metadata only) and the call is served whatever it is.
func TestAssertionVerifyModeLogs(t *testing.T) {
	h, logs, seen, _ := assertionHandler(t, "invalid: uri", false)
	req := httptest.NewRequest(http.MethodGet, "https://"+nodeName+"/salud/afiliados/1000000001?x=1", nil)
	req.Header.Set("X-RID-Assertion", "a.b.c")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || seen.uri != "/salud/afiliados/1000000001?x=1" || seen.header != "a.b.c" || seen.method != "GET" {
		t.Fatalf("%d %+v (the URI checked is the one received, with the service path and the query)", rec.Code, seen)
	}
	if !strings.Contains(logs.String(), `"assertion":"invalid: uri"`) || strings.Contains(logs.String(), "a.b.c") {
		t.Fatalf("the result is logged, never the assertion: %s", logs)
	}
}

func TestAssertionRequireRejects(t *testing.T) {
	h, _, _, _ := assertionHandler(t, "absent", true)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://"+nodeName+"/salud/x", nil))
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "assertion_invalid") || strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("%d %s (the service must not be called)", rec.Code, rec.Body)
	}
	h, _, _, _ = assertionHandler(t, "verified", true)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://"+nodeName+"/salud/x", nil))
	if rec.Code != 200 {
		t.Fatalf("verified: %d", rec.Code)
	}
}

// The body is read for the check and handed to the service unchanged.
func TestAssertionSeesAndRestoresBody(t *testing.T) {
	h, _, seen, got := assertionHandler(t, "verified", false)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "https://"+nodeName+"/salud/v", strings.NewReader(`{"a":1}`)))
	if string(seen.body) != `{"a":1}` || string(*got) != `{"a":1}` || rec.Code != 200 {
		t.Fatalf("checked %q, served %q, %d", seen.body, *got, rec.Code)
	}
}

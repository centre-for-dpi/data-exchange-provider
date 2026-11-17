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
	req := httptest.NewRequest(http.MethodPost, "https://"+nodeName+"/salud/v", strings.NewReader(`{"a":1}`))
	req.Header.Set("X-RID-Assertion", "a.b.c")
	h.ServeHTTP(rec, req)
	if string(seen.body) != `{"a":1}` || string(*got) != `{"a":1}` || rec.Code != 200 {
		t.Fatalf("checked %q, served %q, %d", seen.body, *got, rec.Code)
	}
}

func bodyRoute(t *testing.T, require bool, limit int64, calls *int) (http.Handler, *bytes.Buffer, *[]byte) {
	t.Helper()
	logs := &bytes.Buffer{}
	got := &[]byte{}
	h := NewHandlerWith(nodeName, []Route{{Path: "/docs", MaxRequestBytes: limit, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte("ok"))
	})}}, slog.New(slog.NewJSONHandler(logs, nil)), Options{
		Assertion:        func(_ context.Context, _, _, _ string, _ []byte) string { *calls++; return "verified" },
		RequireAssertion: require,
	})
	return h, logs, got
}

// Final review 1: verify (the default) never rejects and never buffers a body it does not check.
func TestVerifyModeWithoutAssertionStreamsTheBody(t *testing.T) {
	calls := 0
	h, logs, got := bodyRoute(t, false, 0, &calls)
	big := strings.Repeat("x", 9<<20) // over the 8 MiB the check could buffer
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "https://"+nodeName+"/docs/v", strings.NewReader(big)))
	if rec.Code != 200 || len(*got) != len(big) || calls != 0 || !strings.Contains(logs.String(), `"assertion":"absent"`) {
		t.Fatalf("%d, served %d bytes, %d checks, %s", rec.Code, len(*got), calls, logs)
	}
}

func TestVerifyModeServesBodiesTooLargeToVerify(t *testing.T) {
	calls := 0
	h, logs, got := bodyRoute(t, false, 1024, &calls)
	body := strings.Repeat("y", 4096)
	req := httptest.NewRequest(http.MethodPost, "https://"+nodeName+"/docs/v", strings.NewReader(body))
	req.Header.Set("X-RID-Assertion", "a.b.c")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || string(*got) != body || !strings.Contains(logs.String(), `"assertion":"invalid: body too large to verify"`) {
		t.Fatalf("%d, served %d bytes: %s", rec.Code, len(*got), logs)
	}
	calls = 0
	h, _, got = bodyRoute(t, true, 1024, &calls)
	req = httptest.NewRequest(http.MethodPost, "https://"+nodeName+"/docs/v", strings.NewReader(body))
	req.Header.Set("X-RID-Assertion", "a.b.c")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || len(*got) != 0 {
		t.Fatalf("require: %d", rec.Code)
	}
}

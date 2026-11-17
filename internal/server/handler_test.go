package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestHandler(routes []Route) (http.Handler, *syncBuffer) {
	logs := &syncBuffer{}
	return NewHandler(nodeName, routes, slog.New(slog.NewJSONHandler(logs, nil))), logs
}

func request(h http.Handler, host, target string, header map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", target, nil)
	r.Host = host
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func problemCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type %q", ct)
	}
	var p struct {
		Code   string `json:"code"`
		Status int    `json:"status"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || p.Status != w.Code || p.Title == "" {
		t.Fatalf("bad problem: %s", w.Body)
	}
	return p.Code
}

var okRoute = Route{Path: "/svc", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	SetRoute(r, "/svc/afiliados/{id}")
	_, _ = w.Write([]byte(r.URL.Path))
})}

func TestWrongHost421(t *testing.T) {
	h, _ := newTestHandler([]Route{okRoute})
	w := request(h, "other-host", "/svc/x", nil)
	if w.Code != http.StatusMisdirectedRequest || problemCode(t, w) != "misdirected_request" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestHostWithPortAccepted(t *testing.T) {
	h, _ := newTestHandler([]Route{okRoute})
	if w := request(h, nodeName+":8443", "/svc/x", nil); w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
}

func TestUnknownPath404Problem(t *testing.T) {
	h, _ := newTestHandler([]Route{okRoute})
	w := request(h, nodeName, "/svcx/1", nil)
	if w.Code != 404 || problemCode(t, w) != "not_found" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestRoutesByLongestPrefixAndStripsIt(t *testing.T) {
	mk := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(name + ":" + r.URL.Path)) })
	}
	h, _ := newTestHandler([]Route{{Path: "/a", Handler: mk("A")}, {Path: "/a/b", Handler: mk("B")}})
	for target, want := range map[string]string{"/a/b/c": "B:/c", "/a/x": "A:/x", "/a": "A:/", "/a/b": "B:/"} {
		if got := request(h, nodeName, target, nil).Body.String(); got != want {
			t.Fatalf("%s → %q, want %q", target, got, want)
		}
	}
}

func TestAccessLogHasTemplateNotPath(t *testing.T) {
	h, logs := newTestHandler([]Route{okRoute})
	request(h, nodeName, "/svc/afiliados/1000000001?x=1", map[string]string{"X-Request-Id": "0b9e1a6c-2f4d-4e8a-9c1b-7d3e5f6a8b90"})
	line := logs.String()
	if strings.Contains(line, "1000000001") || strings.Contains(line, "x=1") {
		t.Fatalf("the access log leaks the path or query: %s", line)
	}
	for _, want := range []string{`"msg":"access"`, `"route":"/svc/afiliados/{id}"`, `"query_present":true`, `"request_id":"0b9e1a6c-2f4d-4e8a-9c1b-7d3e5f6a8b90"`, `"status":200`, `"method":"GET"`} {
		if !strings.Contains(line, want) {
			t.Fatalf("log lacks %s: %s", want, line)
		}
	}
}

func TestDefaultRouteIsServiceWildcard(t *testing.T) {
	h, logs := newTestHandler([]Route{{Path: "/certificados", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})}})
	request(h, nodeName, "/certificados/v1/123", nil)
	if !strings.Contains(logs.String(), `"route":"/certificados/*"`) {
		t.Fatalf("log: %s", logs)
	}
}

func TestDenHeaderNamesOnly(t *testing.T) {
	h, logs := newTestHandler([]Route{okRoute})
	request(h, nodeName, "/svc/x", map[string]string{"X-DEN-Purpose": "secreto-de-prueba", "X-DEN-Subject-Ref": "abc"})
	line := logs.String()
	if strings.Contains(line, "secreto-de-prueba") || !strings.Contains(line, `"den_headers":["x-den-purpose","x-den-subject-ref"]`) {
		t.Fatalf("log: %s", line)
	}
}

func TestMaxInFlight503(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	slow := Route{Path: "/slow", MaxInFlight: 1, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
	})}
	h, _ := newTestHandler([]Route{slow})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); request(h, nodeName, "/slow/1", nil) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first request never entered")
	}
	w := request(h, nodeName, "/slow/2", nil)
	close(release)
	wg.Wait()
	if w.Code != http.StatusServiceUnavailable || problemCode(t, w) != "service_busy" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// Final review #1: dot segments and encoded separators must never reach a route, or they can
// climb out of the upstream's base path once the internal API normalizes them.
func TestRejectsUnsafePaths(t *testing.T) {
	h, _ := newTestHandler([]Route{okRoute})
	for _, target := range []string{
		"/svc/../../admin", "/svc/./x", "/svc//x", "/svc/%2e%2e/%2e%2e/admin", "/svc/%2E%2E/admin",
		"/svc/x%2Fy", "/svc/x%2fy", "/svc/x%5cy", "/svc/x\\y",
	} {
		w := request(h, nodeName, target, nil)
		if w.Code != http.StatusBadRequest || problemCode(t, w) != "invalid_path" {
			t.Fatalf("%s → %d %s, want 400 invalid_path", target, w.Code, w.Body)
		}
	}
	if w := request(h, nodeName, "/svc/x/", nil); w.Code != 200 {
		t.Fatalf("a trailing slash is a normal path: %d", w.Code)
	}
}

// Final review: the request id is consumer-controlled; only a UUID is logged verbatim.
func TestRequestIDLoggedOnlyIfUUID(t *testing.T) {
	h, logs := newTestHandler([]Route{okRoute})
	request(h, nodeName, "/svc/x", map[string]string{"X-Request-Id": "1000000001"})
	if strings.Contains(logs.String(), "1000000001") {
		t.Fatalf("a non-UUID request id reached the log: %s", logs)
	}
	request(h, nodeName, "/svc/x", map[string]string{"X-Request-Id": "f7f28bb1-558f-4e3b-b08d-c914ae41971f"})
	if !strings.Contains(logs.String(), `"request_id":"f7f28bb1-558f-4e3b-b08d-c914ae41971f"`) {
		t.Fatalf("a UUID request id must be logged: %s", logs)
	}
}

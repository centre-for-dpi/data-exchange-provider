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
	request(h, nodeName, "/svc/afiliados/1000000001?x=1", map[string]string{"X-Request-Id": "req-1"})
	line := logs.String()
	if strings.Contains(line, "1000000001") || strings.Contains(line, "x=1") {
		t.Fatalf("the access log leaks the path or query: %s", line)
	}
	for _, want := range []string{`"msg":"access"`, `"route":"/svc/afiliados/{id}"`, `"query_present":true`, `"request_id":"req-1"`, `"status":200`, `"method":"GET"`} {
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

func TestRidHeaderNamesOnly(t *testing.T) {
	h, logs := newTestHandler([]Route{okRoute})
	request(h, nodeName, "/svc/x", map[string]string{"X-RID-Purpose": "secreto-de-prueba", "X-RID-Subject-Ref": "abc"})
	line := logs.String()
	if strings.Contains(line, "secreto-de-prueba") || !strings.Contains(line, `"rid_headers":["x-rid-purpose","x-rid-subject-ref"]`) {
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

package assertion

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type signer struct {
	key *ecdsa.PrivateKey
	kid string
}

func newSigner(t *testing.T, kid string) signer {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return signer{key: k, kid: kid}
}

func (s signer) jwk() map[string]string {
	pad := func(b *big.Int) string { return base64.RawURLEncoding.EncodeToString(b.FillBytes(make([]byte, 32))) }
	return map[string]string{"kty": "EC", "crv": "P-256", "kid": s.kid, "x": pad(s.key.X), "y": pad(s.key.Y), "alg": "ES256", "use": "sig"}
}

func hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// sign builds what the gateway's den-assertion plugin sends.
func (s signer) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	input := enc(map[string]string{"alg": "ES256", "typ": "JWT", "kid": s.kid}) + "." + enc(claims)
	digest := sha256.Sum256([]byte(input))
	r, sv, err := ecdsa.Sign(rand.Reader, s.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := append(r.FillBytes(make([]byte, 32)), sv.FillBytes(make([]byte, 32))...)
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

type fixture struct {
	v       *Verifier
	signer  signer
	now     time.Time
	fetches *atomic.Int32
	keys    *atomic.Value // []map[string]string
	failing *atomic.Bool  // the JWKS answers 500
	delay   *atomic.Int64 // the JWKS answers after this many milliseconds
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{signer: newSigner(t, "k1"), now: time.Unix(1_800_000_000, 0), fetches: &atomic.Int32{}, keys: &atomic.Value{},
		failing: &atomic.Bool{}, delay: &atomic.Int64{}}
	f.keys.Store([]map[string]string{f.signer.jwk()})
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.fetches.Add(1)
		time.Sleep(time.Duration(f.delay.Load()) * time.Millisecond)
		if f.failing.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": f.keys.Load()})
	}))
	t.Cleanup(jwks.Close)
	f.v = New(Options{JWKSURL: jwks.URL, Audience: "salud", Issuer: "https://gw.red.example", HTTP: jwks.Client()})
	var mu sync.Mutex
	f.v.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return f.now }
	f.v.minFetchGap = 0
	return f
}

func (f *fixture) claims(method, uri string) map[string]any {
	return map[string]any{"iss": "https://gw.red.example", "aud": "salud", "exp": f.now.Unix() + 30, "iat": f.now.Unix(),
		"jti": "req-" + uri + method, "req": map[string]any{"method": method, "uri_sha256": hash(uri)}}
}

func TestVerified(t *testing.T) {
	f := setup(t)
	tok := f.signer.sign(t, f.claims("GET", "/salud/afiliados/1000000001?x=1"))
	if got := f.v.Check(context.Background(), tok, "GET", "/salud/afiliados/1000000001?x=1", nil); got != Verified {
		t.Fatalf("%q", got)
	}
}

func TestAbsentAndMalformed(t *testing.T) {
	f := setup(t)
	if got := f.v.Check(context.Background(), "", "GET", "/x", nil); got != Absent {
		t.Fatalf("%q", got)
	}
	for _, bad := range []string{"a.b", "x.y.z", "...."} {
		if got := f.v.Check(context.Background(), bad, "GET", "/x", nil); !strings.HasPrefix(string(got), "invalid") {
			t.Fatalf("%q → %q", bad, got)
		}
	}
}

func TestRejections(t *testing.T) {
	f := setup(t)
	uri := "/salud/afiliados/1000000001"
	cases := map[string]func(c map[string]any) (method, uri string, tamper bool){
		"audience": func(c map[string]any) (string, string, bool) { c["aud"] = "otra"; return "GET", uri, false },
		"issuer":   func(c map[string]any) (string, string, bool) { c["iss"] = "https://otro"; return "GET", uri, false },
		"expired":  func(c map[string]any) (string, string, bool) { c["exp"] = f.now.Unix() - 120; return "GET", uri, false },
		"method":   func(c map[string]any) (string, string, bool) { return "DELETE", uri, false },
		"uri":      func(c map[string]any) (string, string, bool) { return "GET", uri + "9", false },
		"signature": func(c map[string]any) (string, string, bool) {
			return "GET", uri, true
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := f.claims("GET", uri)
			c["jti"] = name
			method, got, tamper := mutate(c)
			tok := f.signer.sign(t, c)
			if tamper {
				tok = tok[:len(tok)-4] + "AAAA"
			}
			if r := f.v.Check(context.Background(), tok, method, got, nil); !strings.HasPrefix(string(r), "invalid") || !strings.Contains(string(r), name) {
				t.Fatalf("%q", r)
			}
		})
	}
}

func TestBodyIsBound(t *testing.T) {
	f := setup(t)
	c := f.claims("POST", "/certificados/v1")
	c["req"].(map[string]any)["body_sha256"] = hash(`{"a":1}`)
	tok := f.signer.sign(t, c)
	if got := f.v.Check(context.Background(), tok, "POST", "/certificados/v1", []byte(`{"a":2}`)); got != "invalid: body" {
		t.Fatalf("altered body: %q", got)
	}
	c["jti"] = "other"
	tok = f.signer.sign(t, c)
	if got := f.v.Check(context.Background(), tok, "POST", "/certificados/v1", []byte(`{"a":1}`)); got != Verified {
		t.Fatalf("%q", got)
	}
	// A body that arrived must be bound even when the method usually has none.
	g := f.claims("GET", "/x")
	if got := f.v.Check(context.Background(), f.signer.sign(t, g), "GET", "/x", []byte("smuggled")); got != "invalid: body" {
		t.Fatalf("unbound body: %q", got)
	}
}

func TestReplayRejected(t *testing.T) {
	f := setup(t)
	tok := f.signer.sign(t, f.claims("GET", "/x"))
	if got := f.v.Check(context.Background(), tok, "GET", "/x", nil); got != Verified {
		t.Fatalf("%q", got)
	}
	if got := f.v.Check(context.Background(), tok, "GET", "/x", nil); got != "invalid: replayed" {
		t.Fatalf("the same jti twice: %q", got)
	}
}

// A rotated key is fetched once on its first use; an unknown kid does not hammer the portal.
func TestRotationAndUnknownKid(t *testing.T) {
	f := setup(t)
	k2 := newSigner(t, "k2")
	f.keys.Store([]map[string]string{f.signer.jwk(), k2.jwk()})
	if got := f.v.Check(context.Background(), k2.sign(t, f.claims("GET", "/r")), "GET", "/r", nil); got != Verified {
		t.Fatalf("rotated key: %q", got)
	}
	f.v.minFetchGap = time.Minute
	before := f.fetches.Load()
	forged := newSigner(t, "nope")
	for i := 0; i < 5; i++ {
		c := f.claims("GET", "/r")
		c["jti"] = string(rune('a' + i))
		if got := f.v.Check(context.Background(), forged.sign(t, c), "GET", "/r", nil); got != "invalid: unknown kid" {
			t.Fatalf("%q", got)
		}
	}
	if n := f.fetches.Load() - before; n > 1 {
		t.Fatalf("an unknown kid fetched the JWKS %d times within refreshEvery", n)
	}
}

// Final review 2: calls that arrive while the JWKS is being fetched wait for it instead of
// failing with "unknown kid" (in require mode, a burst of 401 at every start).
func TestConcurrentCallsDuringFetch(t *testing.T) {
	f := setup(t)
	f.delay.Store(200)
	var wg sync.WaitGroup
	results := make([]Result, 8)
	for i := range results {
		c := f.claims("GET", "/x")
		c["jti"] = fmt.Sprintf("c%d", i)
		tok := f.signer.sign(t, c)
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i] = f.v.Check(context.Background(), tok, "GET", "/x", nil) }(i)
	}
	wg.Wait()
	for i, r := range results {
		if r != Verified {
			t.Fatalf("call %d during the fetch: %q", i, r)
		}
	}
	if n := f.fetches.Load(); n != 1 {
		t.Fatalf("one fetch for all of them, got %d", n)
	}
}

// Final review 3: a key the portal revokes (removed from the JWKS) stops verifying within
// maxKeyAge; a failed or empty JWKS never wipes the keys the node trusts.
func TestKeysRefreshedAndKeptOnFailure(t *testing.T) {
	f := setup(t)
	tok := func(jti string) string { c := f.claims("GET", "/r"); c["jti"] = jti; return f.signer.sign(t, c) }
	if got := f.v.Check(context.Background(), tok("a"), "GET", "/r", nil); got != Verified {
		t.Fatalf("%q", got)
	}
	f.failing.Store(true)
	f.now = f.now.Add(11 * time.Minute)
	if got := f.v.Check(context.Background(), tok("b"), "GET", "/r", nil); got != Verified {
		t.Fatalf("a failed refresh must keep the keys: %q", got)
	}
	f.failing.Store(false)
	f.keys.Store([]map[string]string{})
	f.now = f.now.Add(11 * time.Minute)
	if got := f.v.Check(context.Background(), tok("c"), "GET", "/r", nil); got != Verified {
		t.Fatalf("an empty JWKS must keep the keys: %q", got)
	}
	f.keys.Store([]map[string]string{newSigner(t, "k2").jwk()}) // k1 revoked
	f.now = f.now.Add(11 * time.Minute)
	if got := f.v.Check(context.Background(), tok("d"), "GET", "/r", nil); got != "invalid: unknown kid" {
		t.Fatalf("a revoked key must stop verifying: %q", got)
	}
}

// A failed fetch is retried after minFetchGap (5 s), not 30 s.
func TestFailedFetchRetriedSoon(t *testing.T) {
	f := setup(t)
	f.v.minFetchGap = 5 * time.Second
	f.failing.Store(true)
	tok := func(jti string) string { c := f.claims("GET", "/r"); c["jti"] = jti; return f.signer.sign(t, c) }
	if got := f.v.Check(context.Background(), tok("a"), "GET", "/r", nil); got != "invalid: unknown kid" {
		t.Fatalf("%q", got)
	}
	f.failing.Store(false)
	f.now = f.now.Add(6 * time.Second)
	if got := f.v.Check(context.Background(), tok("b"), "GET", "/r", nil); got != Verified {
		t.Fatalf("retried after 6 s: %q", got)
	}
}

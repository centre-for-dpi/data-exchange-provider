// Package assertion verifies the gateway's signed assertion (X-RID-Assertion, the portal's
// ADR-0024): a compact JWS ES256 per call, bound to the method, the URI the node receives and its
// body, for this provider entity, with the gateway's keys from the portal's JWKS.
package assertion

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Result of a check: Verified, Absent, or "invalid: <reason>". Reasons never carry values.
type Result string

const (
	Verified Result = "verified"
	Absent   Result = "absent"
)

func invalid(reason string) Result { return Result("invalid: " + reason) }

// skew is the clock difference tolerated on exp.
const skew = 60 * time.Second

// Options configure a Verifier.
type Options struct {
	JWKSURL  string       // the portal's GET /api/v1/gateway/jwks
	Audience string       // this provider entity's slug (the assertion's aud)
	Issuer   string       // the gateway's public URL (iss); unchecked when empty
	HTTP     *http.Client // toward the portal (corporate proxy and roots)
}

// Verifier checks assertions. Safe for concurrent use.
type Verifier struct {
	opts         Options
	now          func() time.Time
	refreshEvery time.Duration // the least time between two JWKS fetches for an unknown kid

	mu          sync.Mutex
	keys        map[string]*ecdsa.PublicKey
	lastRefresh time.Time
	seen        map[string]time.Time // jti → until when it is valid (replay window)
	swept       time.Time
}

// New builds a Verifier; it fetches the JWKS on the first assertion it sees.
func New(o Options) *Verifier {
	return &Verifier{opts: o, now: time.Now, refreshEvery: 30 * time.Second,
		keys: map[string]*ecdsa.PublicKey{}, seen: map[string]time.Time{}}
}

type claims struct {
	Iss string `json:"iss"`
	Aud string `json:"aud"`
	Exp int64  `json:"exp"`
	Jti string `json:"jti"`
	Req struct {
		Method     string  `json:"method"`
		URISHA256  string  `json:"uri_sha256"`
		BodySHA256 *string `json:"body_sha256"`
	} `json:"req"`
}

// Check verifies header for a request (method, the URI as received with its query, the body).
func (v *Verifier) Check(ctx context.Context, header, method, uri string, body []byte) Result {
	if header == "" {
		return Absent
	}
	parts := strings.Split(header, ".")
	if len(parts) != 3 {
		return invalid("not a compact JWS")
	}
	var jose struct{ Alg, Kid string }
	if !decode(parts[0], &jose) || jose.Alg != "ES256" || jose.Kid == "" {
		return invalid("header")
	}
	key := v.key(ctx, jose.Kid)
	if key == nil {
		return invalid("unknown kid")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		return invalid("signature")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(key, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return invalid("signature")
	}
	var c claims
	if !decode(parts[1], &c) {
		return invalid("claims")
	}
	now := v.now()
	switch {
	case c.Aud != v.opts.Audience:
		return invalid("audience")
	case v.opts.Issuer != "" && c.Iss != v.opts.Issuer:
		return invalid("issuer")
	case time.Unix(c.Exp, 0).Add(skew).Before(now):
		return invalid("expired")
	case c.Req.Method != strings.ToUpper(method):
		return invalid("method")
	case c.Req.URISHA256 != sum(uri):
		return invalid("uri")
	}
	m := strings.ToUpper(method)
	if m == http.MethodPost || m == http.MethodPut || m == http.MethodPatch || c.Req.BodySHA256 != nil || len(body) > 0 {
		if c.Req.BodySHA256 == nil || *c.Req.BodySHA256 != sum(string(body)) {
			return invalid("body")
		}
	}
	if c.Jti == "" || !v.remember(c.Jti, time.Unix(c.Exp, 0).Add(skew), now) {
		return invalid("replayed")
	}
	return Verified
}

func sum(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func decode(part string, v any) bool {
	b, err := base64.RawURLEncoding.DecodeString(part)
	return err == nil && json.Unmarshal(b, v) == nil
}

func (v *Verifier) remember(jti string, until, now time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if now.Sub(v.swept) >= time.Second { // at most once a second: under load the map holds a minute of calls
		for id, end := range v.seen {
			if end.Before(now) {
				delete(v.seen, id)
			}
		}
		v.swept = now
	}
	if _, dup := v.seen[jti]; dup {
		return false
	}
	v.seen[jti] = until
	return true
}

// key is the public key for kid, fetching the JWKS when the kid is unknown (at most once per
// refreshEvery, so a forged kid cannot make the node hammer the portal).
func (v *Verifier) key(ctx context.Context, kid string) *ecdsa.PublicKey {
	v.mu.Lock()
	k, ok := v.keys[kid]
	due := v.lastRefresh.IsZero() || v.now().Sub(v.lastRefresh) >= v.refreshEvery
	if !ok && due {
		v.lastRefresh = v.now()
	}
	v.mu.Unlock()
	if ok || !due {
		return k
	}
	keys, err := v.fetch(ctx)
	if err != nil {
		return nil
	}
	v.mu.Lock()
	v.keys = keys
	v.mu.Unlock()
	return keys[kid]
}

func (v *Verifier) fetch(ctx context.Context) (map[string]*ecdsa.PublicKey, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.opts.JWKSURL, nil)
	if err != nil {
		return nil, err
	}
	res, err := v.opts.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS %d", res.StatusCode)
	}
	var set struct {
		Keys []struct{ Kty, Crv, Kid, X, Y string } `json:"keys"`
	}
	if err := json.NewDecoder(res.Body).Decode(&set); err != nil {
		return nil, err
	}
	keys := map[string]*ecdsa.PublicKey{}
	for _, k := range set.Keys {
		x, errX := base64.RawURLEncoding.DecodeString(k.X)
		y, errY := base64.RawURLEncoding.DecodeString(k.Y)
		if k.Kty != "EC" || k.Crv != "P-256" || k.Kid == "" || errX != nil || errY != nil {
			continue
		}
		pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		if !pub.Curve.IsOnCurve(pub.X, pub.Y) { //nolint:staticcheck // a key off the curve is refused
			continue
		}
		keys[k.Kid] = pub
	}
	return keys, nil
}

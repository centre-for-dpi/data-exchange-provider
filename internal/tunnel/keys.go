package tunnel

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"

	"golang.org/x/crypto/curve25519"
)

// Key is a WireGuard (Curve25519) key.
type Key [32]byte

// GenerateKey returns a new clamped private key and its public key.
func GenerateKey() (priv, pub Key) {
	_, _ = rand.Read(priv[:])
	priv[0] &= 248
	priv[31] = (priv[31] & 127) | 64
	return priv, PublicKey(priv)
}

// PublicKey derives the public key of a private key.
func PublicKey(priv Key) (pub Key) {
	b, _ := curve25519.X25519(priv[:], curve25519.Basepoint)
	copy(pub[:], b)
	return pub
}

// ParseKey reads a base64 key, as the contract and wg(8) write them.
func ParseKey(s string) (Key, error) {
	var k Key
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != len(k) {
		return k, errors.New("clave WireGuard inválida")
	}
	copy(k[:], b)
	return k, nil
}

// String is the base64 form.
func (k Key) String() string { return base64.StdEncoding.EncodeToString(k[:]) }

func (k Key) hex() string { return hex.EncodeToString(k[:]) }

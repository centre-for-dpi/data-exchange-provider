package config

import (
	"fmt"
	"os"
	"strings"
)

// ReadSecret reads a secret file (Compose secrets are mounted under /run/secrets) and trims the
// trailing newline an editor leaves. An empty secret is an error, never a silent empty value.
func ReadSecret(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("no se pudo leer el secreto %s: %w", path, err)
	}
	s := strings.TrimRight(string(b), "\r\n")
	if s == "" {
		return "", fmt.Errorf("el secreto %s está vacío", path)
	}
	return s, nil
}

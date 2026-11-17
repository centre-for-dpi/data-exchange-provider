package config

import (
	"os"
	"strings"

	"github.com/centre-for-dpi/data-exchange-provider/internal/i18n"
)

// ReadSecret reads a secret file (Compose secrets are mounted under /run/secrets) and trims the
// trailing newline an editor leaves. An empty secret is an error, never a silent empty value.
func ReadSecret(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", i18n.Errorf("cannot read the secret %s: %w", path, err)
	}
	s := strings.TrimRight(string(b), "\r\n")
	if s == "" {
		return "", i18n.Errorf("the secret %s is empty", path)
	}
	return s, nil
}

// Package i18n holds the node's user-facing messages: what an operator reads from the CLI,
// doctor, status and errors, the problem titles the node answers, and the texts of the OpenAPI
// it generates. The English text is written in the code and is the key of the Spanish catalog
// (catalog_es.go). DEN_LANG chooses the language: en (default) or es. Logs stay in English.
package i18n

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
)

// Lang is a supported language.
type Lang string

const (
	EN Lang = "en"
	ES Lang = "es"
)

var current atomic.Value // Lang

func init() { current.Store(EN) }

// Current is the language messages are written in.
func Current() Lang { return current.Load().(Lang) }

// Set chooses the language: "en" or "es" (any case, with or without a region such as es_CO).
func Set(lang string) error {
	l := strings.ToLower(strings.TrimSpace(lang))
	if i := strings.IndexAny(l, "_-."); i > 0 {
		l = l[:i]
	}
	switch Lang(l) {
	case EN, ES:
		current.Store(Lang(l))
		return nil
	}
	return fmt.Errorf("unsupported language %q: use en or es", lang)
}

// FromEnv applies DEN_LANG. An unknown value keeps English and returns a warning to log.
func FromEnv() (warning string) {
	v := os.Getenv("DEN_LANG")
	if v == "" {
		current.Store(EN)
		return ""
	}
	if err := Set(v); err != nil {
		current.Store(EN)
		return err.Error()
	}
	return ""
}

// T is msg in the current language. A message without a translation stays in English.
func T(msg string) string {
	if Current() == ES {
		if tr, ok := es[msg]; ok {
			return tr
		}
	}
	return msg
}

// Sprintf formats a catalog message.
func Sprintf(format string, a ...any) string { return fmt.Sprintf(T(format), a...) }

// Errorf is fmt.Errorf with a catalog message (%w wraps as usual).
func Errorf(format string, a ...any) error { return fmt.Errorf(T(format), a...) }

// New is errors.New with a catalog message.
func New(msg string) error { return errors.New(T(msg)) }

// Fprintf writes a formatted catalog message.
func Fprintf(w io.Writer, format string, a ...any) (int, error) {
	return fmt.Fprintf(w, T(format), a...)
}

// Fprintln writes a catalog message and a newline.
func Fprintln(w io.Writer, msg string) (int, error) { return fmt.Fprintln(w, T(msg)) }

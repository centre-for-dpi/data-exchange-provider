//go:build canal

package config

import "testing"

func TestInstantRejectedInCanal(t *testing.T) {
	mustFail(t, "portal: https://portal.red.example\nservices:\n  - { name: afiliacion, path: /salud, type: instant }\n", "modo canal")
}

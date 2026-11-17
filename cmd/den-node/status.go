package main

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/agent"
	"github.com/centre-for-dpi/data-exchange-provider/internal/i18n"
	"github.com/centre-for-dpi/data-exchange-provider/internal/state"
)

// status prints status.json, in the operator's language, or raw with asJSON.
func status(stateDir string, asJSON bool, out io.Writer, now func() time.Time) int {
	var st agent.Status
	if err := (state.Store{Dir: stateDir}).ReadStatus(&st); err != nil {
		i18n.Fprintln(out, "The node has not written its status yet: is it running? Has it enrolled? Check the logs with install.sh logs.")
		return 1
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(st)
		return 0
	}
	ago := func(t time.Time) string {
		if t.IsZero() {
			return i18n.T("never")
		}
		return i18n.Sprintf("%s ago", now().Sub(t).Round(time.Second).String())
	}
	i18n.Fprintf(out, "Node:         %s\n", st.NodeName)
	i18n.Fprintf(out, "Portal:       %s (last heartbeat %s)\n", st.PortalState, ago(st.LastHeartbeat))
	i18n.Fprintf(out, "Tunnel:       last handshake %s\n", ago(st.LastHandshake))
	i18n.Fprintf(out, "Certificate:  expires %s\n", st.CertificateNotAfter)
	health := st.Health.Status
	if st.Health.Detail != "" {
		health += " — " + st.Health.Detail
	}
	i18n.Fprintf(out, "Health:       %s\n", health)
	i18n.Fprintln(out, "Services:")
	if len(st.Services) == 0 {
		i18n.Fprintln(out, "  (none: add one with install.sh service)")
	}
	for _, s := range st.Services {
		mark, detail := "✔", ""
		if !s.Healthy {
			mark, detail = "✘", " — "+s.Detail
		}
		fmt.Fprintf(out, "  %s %-16s %s%s\n", mark, s.Name, s.Path, detail)
		if s.Requests > 0 {
			i18n.Fprintf(out, "      %d requests (4xx: %d, 5xx: %d) · p50 %.0f ms · p95 %.0f ms\n",
				s.Requests, s.ClientErrors, s.ServerErrors, s.P50Ms, s.P95Ms)
		}
	}
	if len(st.Notices) > 0 {
		i18n.Fprintln(out, "Notices:")
		for _, n := range st.Notices {
			fmt.Fprintf(out, "  ⚠ %s\n", n)
		}
	}
	fmt.Fprintf(out, "Kit:          %s (%s)\n", st.KitVersion, st.Variant)
	return 0
}

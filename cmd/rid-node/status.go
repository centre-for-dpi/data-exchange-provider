package main

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/agent"
	"github.com/centre-for-dpi/data-exchange-provider/internal/state"
)

// status prints status.json, in Spanish, or raw with asJSON.
func status(stateDir string, asJSON bool, out io.Writer, now func() time.Time) int {
	var st agent.Status
	if err := (state.Store{Dir: stateDir}).ReadStatus(&st); err != nil {
		fmt.Fprintln(out, "El nodo todavía no escribió su estado: ¿está corriendo? ¿ya se enroló? Mira los logs con install.sh logs.")
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
			return "nunca"
		}
		return "hace " + now().Sub(t).Round(time.Second).String()
	}
	fmt.Fprintf(out, "Nodo:         %s\n", st.NodeName)
	fmt.Fprintf(out, "Portal:       %s (último heartbeat %s)\n", st.PortalState, ago(st.LastHeartbeat))
	fmt.Fprintf(out, "Túnel:        último handshake %s\n", ago(st.LastHandshake))
	fmt.Fprintf(out, "Certificado:  vence %s\n", st.CertificateNotAfter)
	health := st.Health.Status
	if st.Health.Detail != "" {
		health += " — " + st.Health.Detail
	}
	fmt.Fprintf(out, "Salud:        %s\n", health)
	fmt.Fprintln(out, "Servicios:")
	if len(st.Services) == 0 {
		fmt.Fprintln(out, "  (ninguno: agrega uno con install.sh servicio)")
	}
	for _, s := range st.Services {
		mark, detail := "✔", ""
		if !s.Healthy {
			mark, detail = "✘", " — "+s.Detail
		}
		fmt.Fprintf(out, "  %s %-16s %s%s\n", mark, s.Name, s.Path, detail)
		if s.Requests > 0 {
			fmt.Fprintf(out, "      %d peticiones (4xx: %d, 5xx: %d) · p50 %.0f ms · p95 %.0f ms\n",
				s.Requests, s.ClientErrors, s.ServerErrors, s.P50Ms, s.P95Ms)
		}
	}
	if len(st.Notices) > 0 {
		fmt.Fprintln(out, "Avisos:")
		for _, n := range st.Notices {
			fmt.Fprintf(out, "  ⚠ %s\n", n)
		}
	}
	fmt.Fprintf(out, "Kit:          %s (%s)\n", st.KitVersion, st.Variant)
	return 0
}

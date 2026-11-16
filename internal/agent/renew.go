package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"

	"github.com/centre-for-dpi/data-exchange-provider/internal/contract"
	"github.com/centre-for-dpi/data-exchange-provider/internal/state"
)

// renew asks for a certificate for a new key. The key and CSR are persisted as a pending
// renewal before the call: if the answer is lost, the next attempt repeats the same CSR, which
// the portal accepts from the replaced certificate during its grace period (contract v1).
func (c *Controller) renew(ctx context.Context) {
	if c.st.PendingRenewal == nil {
		keyPEM, csrPEM, err := NewKeyAndCSR()
		if err != nil {
			c.log.Error("renew_failed", "error", err.Error())
			return
		}
		c.st.PendingRenewal = &state.PendingRenewal{KeyPEM: keyPEM, CSRPEM: csrPEM, CreatedAt: c.Now()}
		if err := c.store.Save(c.st); err != nil {
			c.log.Error("renew_failed", "error", "no se pudo guardar la renovación pendiente: "+err.Error())
			c.st.PendingRenewal = nil
			return
		}
	}
	pending := c.st.PendingRenewal
	res, err := c.control.Renew(ctx, c.st.NodeID, pending.CSRPEM)
	switch status := contract.StatusOf(err); {
	case err == nil:
	case status == http.StatusConflict:
		c.log.Warn("renew_refused", "detail", "el portal no renueva certificados de una entidad suspendida")
		return
	case status == http.StatusBadRequest:
		c.log.Warn("renew_refused", "detail", "el portal rechazó el CSR; la próxima renovación usará una clave nueva")
		c.dropPending()
		return
	case status == http.StatusUnauthorized:
		c.log.Error("renew_refused", "detail", "el portal no acepta el certificado del nodo para renovar: pasó la gracia de 24 h o el nodo fue dado de baja; usa install.sh reenrolar")
		c.dropPending()
		return
	default:
		c.log.Warn("renew_failed", "error", err.Error(), "detail", "se reintenta con el mismo CSR")
		return
	}
	pair, err := tls.X509KeyPair([]byte(res.Certificate), []byte(pending.KeyPEM))
	if err != nil {
		c.log.Error("renew_failed", "error", "el certificado renovado no corresponde a la clave: "+err.Error())
		return
	}
	old := c.cert.Load()
	c.st.Certificate, c.st.CertificateNotAfter, c.st.KeyPEM = res.Certificate, res.CertificateNotAfter, pending.KeyPEM
	c.st.PendingRenewal = nil
	if err := c.store.Save(c.st); err != nil {
		// Keep serving with the old certificate; the pending renewal stays for the next try.
		c.log.Error("renew_failed", "error", "no se pudo guardar el certificado renovado: "+err.Error())
		c.st.PendingRenewal = pending
		return
	}
	c.cert.Store(&pair)
	c.log.Info("certificate_renewed", "old_serial", serialOf(old), "new_serial", serialOf(&pair),
		"not_after", res.CertificateNotAfter, "previous_valid_until", res.PreviousValidUntil)
}

func (c *Controller) dropPending() {
	c.st.PendingRenewal = nil
	if err := c.store.Save(c.st); err != nil {
		c.log.Warn("state_write_failed", "error", err.Error())
	}
}

func serialOf(c *tls.Certificate) string {
	if c == nil || len(c.Certificate) == 0 {
		return ""
	}
	x, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return ""
	}
	return x.SerialNumber.String()
}

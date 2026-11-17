package contract

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// schemaAt compiles the JSON Schema found at a JSON pointer of the portal's contract.
func schemaAt(t *testing.T, pointer string) *jsonschema.Schema {
	t.Helper()
	f, err := os.Open("testdata/node-contract-v1.openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource("contract.json", doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile("contract.json#" + pointer)
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

func escape(path string) string {
	return strings.ReplaceAll(strings.ReplaceAll(path, "~", "~0"), "/", "~1")
}

func requestSchema(t *testing.T, path, method string) *jsonschema.Schema {
	return schemaAt(t, "/paths/"+escape(path)+"/"+method+"/requestBody/content/application~1json/schema")
}

func responseSchema(t *testing.T, path, method, status string) *jsonschema.Schema {
	return schemaAt(t, "/paths/"+escape(path)+"/"+method+"/responses/"+status+"/content/application~1json/schema")
}

func mustValidate(t *testing.T, sch *jsonschema.Schema, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("%s does not match the contract: %v", b, err)
	}
}

const (
	csrPEM = "-----BEGIN CERTIFICATE REQUEST-----\nMIIBCDCBrwIBADANMQswCQYDVQQDDAJpZDBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB\n-----END CERTIFICATE REQUEST-----\n"
	wgKey  = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	nodeID = "01a1e5a6-9381-738c-a707-d6e29330ea26"
)

func TestRequestsMatchContract(t *testing.T) {
	mustValidate(t, requestSchema(t, "/api/v1/nodes/enroll", "post"), EnrollRequest{
		Token: "tok_0123456789abcdefghij", CSR: csrPEM, WireGuardPublicKey: wgKey, KitVersion: "0.1.0-dev", ServicePort: 8443,
	})
	mustValidate(t, requestSchema(t, "/api/v1/nodes/{nodeId}/heartbeat", "post"), HeartbeatRequest{
		KitVersion: "0.1.0", ServicePort: 8443,
		Services: []ServiceInfo{{Name: "certificados", Path: "/certificados"}, {Name: "afiliacion", Path: "/salud", OpenAPISHA256: strings.Repeat("a", 64)}},
		Health:   Health{Status: "degraded", Detail: "upstream certificados no responde"},
	})
	mustValidate(t, requestSchema(t, "/api/v1/nodes/{nodeId}/renew", "post"), RenewRequest{CSR: csrPEM})
}

func TestResponsesDecode(t *testing.T) {
	enroll := `{"contractVersion":"1","nodeId":"` + nodeID + `","nodeName":"den-node-` + nodeID + `",
	  "certificate":"c","certificateNotAfter":"2027-02-14T18:37:54Z","caCertificate":"ca",
	  "controlUrl":"https://localhost:9443","gatewayClientName":"den-gateway.salud","heartbeatIntervalSeconds":60,
	  "wireguard":{"address":"10.77.0.19/32","hubPublicKey":"` + wgKey + `","hubEndpoint":"wg-hub:51820",
	  "hubAddress":"10.77.0.1","allowedIps":["10.77.0.1/32"],"persistentKeepaliveSeconds":25}}`
	mustValidate(t, responseSchema(t, "/api/v1/nodes/enroll", "post", "201"), json.RawMessage(enroll))
	var e EnrollResponse
	if err := json.Unmarshal([]byte(enroll), &e); err != nil {
		t.Fatal(err)
	}
	if e.NodeName != "den-node-"+nodeID || e.WireGuard.AllowedIPs[0] != "10.77.0.1/32" || e.WireGuard.PersistentKeepaliveSeconds != 25 || e.GatewayClientName != "den-gateway.salud" {
		t.Fatalf("decoded: %+v", e)
	}

	hb := `{"status":"online","nextHeartbeatSeconds":60,"renewCertificate":true}`
	mustValidate(t, responseSchema(t, "/api/v1/nodes/{nodeId}/heartbeat", "post", "200"), json.RawMessage(hb))
	var h HeartbeatResponse
	_ = json.Unmarshal([]byte(hb), &h)
	if h.Status != "online" || h.NextHeartbeatSeconds != 60 || !h.RenewCertificate {
		t.Fatalf("decoded: %+v", h)
	}

	renew := `{"certificate":"c2","certificateNotAfter":"2027-05-14T18:37:54Z","previousValidUntil":"2026-11-17T18:37:54Z"}`
	mustValidate(t, responseSchema(t, "/api/v1/nodes/{nodeId}/renew", "post", "200"), json.RawMessage(renew))
	var r RenewResponse
	_ = json.Unmarshal([]byte(renew), &r)
	if r.Certificate != "c2" || r.PreviousValidUntil == "" {
		t.Fatalf("decoded: %+v", r)
	}

	cfg := `{"contractVersion":"1","nodeId":"` + nodeID + `","heartbeatIntervalSeconds":60,"minimumKitVersion":"0.2.0",
	  "services":[{"apiVersionId":"01a1e5a6-f983-71d2-9309-76968b3bacff","apiName":"Afiliación","version":"1.0","basePath":"/salud/afiliacion/v1","servicePath":"/salud"}],
	  "notices":[{"code":"upgrade","message":"Actualiza el kit"}]}`
	mustValidate(t, responseSchema(t, "/api/v1/nodes/{nodeId}/config", "get", "200"), json.RawMessage(cfg))
	var c ConfigResponse
	_ = json.Unmarshal([]byte(cfg), &c)
	if c.MinimumKitVersion == nil || *c.MinimumKitVersion != "0.2.0" || c.Services[0].ServicePath != "/salud" || c.Notices[0].Code != "upgrade" {
		t.Fatalf("decoded: %+v", c)
	}
}

// The schema check is real: a body the portal would refuse fails here too.
func TestContractRejectsWhatThePortalRejects(t *testing.T) {
	b, _ := json.Marshal(HeartbeatRequest{KitVersion: "v1", ServicePort: 8443, Services: []ServiceInfo{{Name: "Bad Name", Path: "x"}}, Health: Health{Status: "fine"}})
	inst, _ := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err := requestSchema(t, "/api/v1/nodes/{nodeId}/heartbeat", "post").Validate(inst); err == nil {
		t.Fatal("an invalid heartbeat body passed the contract schema")
	}
}

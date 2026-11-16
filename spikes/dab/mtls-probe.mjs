// Throwaway: probes DAB's HTTPS endpoint with different client certificates (Node uses OpenSSL;
// the Git Bash curl on this machine is Schannel and cannot load PEM client keys).
import { readFileSync } from 'node:fs';
import https from 'node:https';

const pki = new URL('./pki/', import.meta.url);
const read = (f) => readFileSync(new URL(f, pki));
const url = process.argv[2] ?? 'https://localhost:5443/api/afiliados/numero_documento/1000000001';

const cases = [
  ['no client certificate', {}],
  ['gateway (CN=rid-gateway, spike CA)', { cert: read('gateway.crt'), key: read('gateway.key') }],
  ['intruder (CN=someone-else, spike CA)', { cert: read('intruder.crt'), key: read('intruder.key') }],
  ['foreign (CN=rid-gateway, other CA)', { cert: read('foreign.crt'), key: read('foreign.key') }],
];

for (const [name, opts] of cases) {
  const result = await new Promise((resolve) => {
    const req = https.get(url, { ca: read('ca.crt'), ...opts, agent: false }, (res) => {
      res.resume();
      res.on('end', () => resolve(`HTTP ${res.statusCode}`));
    });
    req.on('error', (e) => resolve(`error: ${e.code ?? ''} ${e.message}`));
    req.setTimeout(5000, () => req.destroy(new Error('timeout')));
  });
  console.log(`${name.padEnd(40)} ${result}`);
}

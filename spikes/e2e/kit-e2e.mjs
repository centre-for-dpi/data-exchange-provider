#!/usr/bin/env node
// THROWAWAY SPIKE (ADR-0001). End to end with the REAL portal and the Go node in a container:
// the entity registers a node and gets a token, the node enrolls and joins the real WireGuard hub,
// a version is published with upstream `node`, passes review, and a consumer calls it through the
// gateway. Helpers (Keycloak login, client certificate, gateway calls) are copied from the
// portal's scripts/demo.mjs. Prerequisites: portal stack up, portal API on :4000, and the spike's
// kit-pg running (spikes/e2e/compose.yaml). Usage: node kit-e2e.mjs <env-file with passwords>
import { spawnSync } from 'node:child_process';
import { createHash, randomBytes, randomUUID, X509Certificate } from 'node:crypto';
import { existsSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { request as httpsRequest } from 'node:https';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { DEFAULT_DEMO_TOTP_SECRET, totp } from '../../../data-exchange/scripts/totp.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../../../data-exchange');
if (existsSync(resolve(root, '.env'))) process.loadEnvFile(resolve(root, '.env'));

const env = (name, fallback) => process.env[name] ?? fallback;
const API = `${env('RID_API_URL', 'http://localhost:4000')}/api/v1`;
const KEYCLOAK = env('KEYCLOAK_URL', 'http://localhost:8080');
const GATEWAY = env('RID_GATEWAY_PUBLIC_URL', 'https://localhost:9443');
// The plain-HTTP listener of the local gateway: only for "does the route exist yet?" (no token).
const GATEWAY_HTTP = env('RID_GATEWAY_HTTP_URL', 'http://localhost:9080');
// With mTLS (M3-10) the gateway is HTTPS and every call presents the application's certificate.
const MTLS = GATEWAY.startsWith('https://');
const WEB = env('RID_WEB_URL', 'http://localhost:3000');
const PASSWORD = env('RID_DEMO_USER_PASSWORD', 'demo');
const TOTP_SECRET = env('RID_DEMO_TOTP_SECRET', DEFAULT_DEMO_TOTP_SECRET);
const RUN = Date.now().toString(36);
const SLUG = `afiliacion-${RUN}`;

// --- Output ----------------------------------------------------------------------------------

const color = (code) => (text) => (process.stdout.isTTY ? `\x1b[${code}m${text}\x1b[0m` : text);
const bold = color('1');
const green = color('32');
const red = color('31');
const dim = color('2');
const started = Date.now();
let stepNo = 0;

function step(title, who) {
  stepNo += 1;
  console.log(`\n${bold(`${stepNo}. ${title}`)}${who ? dim(`  (${who})`) : ''}`);
}
const ok = (text) => console.log(`   ${green('✓')} ${text}`);
const info = (text) => console.log(`   ${dim(text)}`);

class DemoError extends Error {}
function expect(condition, message) {
  if (!condition) throw new DemoError(message);
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/** Repeats `probe` every `everyMs` until it returns a truthy value or `timeoutMs` passes. */
async function waitFor(probe, { timeoutMs, everyMs = 1_000, what }) {
  const until = Date.now() + timeoutMs;
  for (;;) {
    const value = await probe();
    if (value) return value;
    if (Date.now() > until) throw new DemoError(`Timed out after ${timeoutMs / 1000} s: ${what}`);
    await sleep(everyMs);
  }
}

// --- Keycloak login (Authorization Code + PKCE, like the web app) ------------------------------

const PORTAL_CLIENT = {
  realm: 'rid-portal',
  clientId: env('RID_PORTAL_WEB_CLIENT_ID', 'rid-portal-web'),
  secret: env('RID_PORTAL_WEB_CLIENT_SECRET', ''),
  redirectUri: `${WEB}/api/auth/callback/keycloak`,
};
/** The reference citizen folder in the (simulated) national identity (MT-05, MT-07). */
const CITIZEN_CLIENT = {
  realm: 'rid-citizens',
  clientId: 'carpeta-ciudadana',
  secret: env('RID_CARPETA_CLIENT_SECRET', ''),
  redirectUri: `${env('RID_CARPETA_URL', 'http://localhost:3100')}/auth/callback`,
};

/** Seed users with a critical role: their realm entry has a one-time code (M3-08). */
const MFA_USERS = new Set(
  JSON.parse(readFileSync(resolve(root, 'infra/keycloak/rid-portal-realm.json'), 'utf8'))
    .users.filter((u) => u.credentials?.some((c) => c.type === 'otp'))
    .map((u) => u.username),
);
/** Codes already sent per user: Keycloak accepts each code once. */
const usedCodes = new Map();

/**
 * The one-time code step (M3-08). Keycloak accepts the code of this 30 s period and of the ones
 * on either side; a code is good only once, so a character signing in again tries another.
 */
async function submitOtp(username, html, post) {
  const used = usedCodes.get(username) ?? new Set();
  usedCodes.set(username, used);
  for (let attempt = 0; attempt < 8; attempt += 1) {
    const action = /<form[^>]+id="kc-otp-login-form"[^>]+action="([^"]+)"/.exec(html)?.[1];
    expect(action, `Keycloak one-time code form not found for ${username}`);
    const code = [0, 30_000, -30_000]
      .map((d) => totp(TOTP_SECRET, Date.now() + d))
      .find((c) => !used.has(c));
    if (!code) {
      await sleep(30_000 - (Date.now() % 30_000) + 500);
      continue;
    }
    used.add(code);
    const res = await post(action, { otp: code });
    if (res.headers.get('location')) return res;
    html = await res.text();
  }
  throw new DemoError(
    `Keycloak refused the one-time codes of ${username} (is RID_DEMO_TOTP_SECRET right?)`,
  );
}

async function login(username, client = PORTAL_CLIENT) {
  const realm = `${KEYCLOAK}/realms/${client.realm}/protocol/openid-connect`;
  const redirectUri = client.redirectUri;
  const verifier = randomBytes(32).toString('base64url');
  const params = new URLSearchParams({
    client_id: client.clientId,
    response_type: 'code',
    scope: 'openid',
    redirect_uri: redirectUri,
    state: randomUUID(),
    code_challenge: createHash('sha256').update(verifier).digest('base64url'),
    code_challenge_method: 'S256',
  });
  // A critical role needs the second factor: ask for it, as the web does after /me (ADR-0021).
  const mfa = client === PORTAL_CLIENT && MFA_USERS.has(username);
  if (mfa) params.set('acr_values', 'mfa');
  const cookies = new Map();
  const keep = (res) => {
    for (const c of res.headers.getSetCookie()) {
      const [pair] = c.split(';');
      const i = pair.indexOf('=');
      if (i > 0) cookies.set(pair.slice(0, i), pair.slice(i + 1));
    }
  };
  const page = await fetch(`${realm}/auth?${params}`, { redirect: 'manual' });
  keep(page);
  const action = /<form[^>]+id="kc-form-login"[^>]+action="([^"]+)"/.exec(await page.text())?.[1];
  expect(action, `Keycloak login form not found for ${username}`);
  const post = async (target, body) => {
    const res = await fetch(target.replaceAll('&amp;', '&'), {
      method: 'POST',
      redirect: 'manual',
      headers: {
        'content-type': 'application/x-www-form-urlencoded',
        cookie: [...cookies].map(([k, v]) => `${k}=${v}`).join('; '),
      },
      body: new URLSearchParams(body),
    });
    keep(res);
    return res;
  };
  let submitted = await post(action, { username, password: PASSWORD, credentialId: '' });
  if (mfa && !submitted.headers.get('location')) {
    submitted = await submitOtp(username, await submitted.text(), post);
  }
  const code = new URL(submitted.headers.get('location') ?? 'http://x/').searchParams.get('code');
  expect(code, `Login failed for ${username} (is RID_DEMO_USER_PASSWORD right?)`);
  const { clientId, secret } = client;
  const tokens = await fetch(`${realm}/token`, {
    method: 'POST',
    headers: {
      'content-type': 'application/x-www-form-urlencoded',
      authorization: `Basic ${Buffer.from(`${clientId}:${secret}`).toString('base64')}`,
    },
    body: new URLSearchParams({
      grant_type: 'authorization_code',
      code,
      redirect_uri: redirectUri,
      code_verifier: verifier,
    }),
  });
  expect(tokens.ok, `Token exchange failed for ${username}: ${tokens.status}`);
  return (await tokens.json()).access_token;
}

/** A character of DEMO.md: calls the portal API with their own session. */
async function as(username) {
  const token = await login(username);
  const call = async (method, path, body) => {
    const res = await fetch(`${API}${path}`, {
      method,
      headers: {
        authorization: `Bearer ${token}`,
        ...(body === undefined ? {} : { 'content-type': 'application/json' }),
      },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    const data = res.status === 204 ? undefined : await res.json().catch(() => undefined);
    expect(res.ok, `${username}: ${method} ${path} → ${res.status} ${JSON.stringify(data)}`);
    return data;
  };
  const me = await call('GET', '/me');
  const entity = (slug) => {
    const found = me.memberships.find((m) => m.entity.slug === slug);
    expect(found, `${username} does not belong to ${slug}`);
    return found.entity.id;
  };
  return { call, entity, username };
}

// --- Gateway -----------------------------------------------------------------------------------

/** The application's key and certificate, once carlos has one (M3-10), and the network root. */
const tls = { client: undefined, ca: undefined };

/** fetch() for the gateway: over HTTPS, trusting the network CA and with the client certificate. */
async function gatewayFetch(url, { method = 'GET', headers = {}, body } = {}) {
  // Only the gateway's own name uses the network CA (and the client certificate); other HTTPS
  // URLs, such as a proxy's public /gw, have ordinary certificates.
  if (!url.startsWith(GATEWAY) || !GATEWAY.startsWith('https://')) {
    return fetch(url, { method, headers, body });
  }
  tls.ca ??= await (await fetch(`${API}/pki/ca`)).text();
  const target = new URL(url);
  return new Promise((resolveCall, reject) => {
    const req = httpsRequest(
      {
        host: target.hostname,
        port: target.port || 443,
        path: `${target.pathname}${target.search}`,
        method,
        headers,
        ca: tls.ca,
        agent: false,
        ...(tls.client ?? {}),
      },
      (res) => {
        const chunks = [];
        res.on('data', (c) => chunks.push(c));
        res.on('end', () => {
          const h = new Headers();
          for (const [k, v] of Object.entries(res.headers)) {
            for (const one of [v].flat()) if (one !== undefined) h.append(k, one);
          }
          resolveCall(new Response(Buffer.concat(chunks), { status: res.statusCode, headers: h }));
        });
      },
    );
    req.on('error', reject);
    req.end(body);
  });
}

/**
 * carlos's client certificate (M3-10): the key and the CSR are made on "his server" (openssl,
 * here a temporary folder); the portal has the network CA sign it. The key never leaves.
 */
async function clientCertificate(carlos, entityId, applicationId, clientId) {
  const dir = mkdtempSync(join(tmpdir(), 'rid-demo-'));
  try {
    const run = spawnSync(
      'openssl',
      [
        'req',
        '-new',
        '-newkey',
        'ec',
        '-pkeyopt',
        'ec_paramgen_curve:P-256',
        '-nodes',
        '-keyout',
        join(dir, 'app.key'),
        '-out',
        join(dir, 'app.csr'),
        '-subj',
        `/CN=${clientId}`,
      ],
      { encoding: 'utf8', env: { ...process.env, MSYS_NO_PATHCONV: '1' } },
    );
    expect(run.status === 0, `openssl could not make the CSR: ${run.stderr || run.error}`);
    const issued = await carlos.call(
      'POST',
      `/entities/${entityId}/applications/${applicationId}/certificates`,
      { csr: readFileSync(join(dir, 'app.csr'), 'utf8') },
    );
    tls.client = { cert: issued.certificatePem, key: readFileSync(join(dir, 'app.key'), 'utf8') };
    tls.credentialId = issued.credential.id;
    return new X509Certificate(issued.certificatePem);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

/** client_credentials at the gateway's token route: with mTLS, bound to the certificate. */
async function gatewayToken(clientId, clientSecret) {
  const res = await gatewayFetch(`${GATEWAY}/oauth/token`, {
    method: 'POST',
    headers: {
      'content-type': 'application/x-www-form-urlencoded',
      authorization: `Basic ${Buffer.from(`${clientId}:${clientSecret}`).toString('base64')}`,
    },
    body: 'grant_type=client_credentials',
  });
  expect(res.ok, `client_credentials token failed: ${res.status} ${await res.clone().text()}`);
  return (await res.json()).access_token;
}

const PERSON = '1000000001';
const URL_PATH = `/salud/${SLUG}/v1/afiliados/${PERSON}`;

async function callGateway(token, headers = {}) {
  // Without a token there is nothing to authenticate: the plain-HTTP listener says whether the
  // route exists (with mTLS, HTTPS without a certificate has no TLS session at all).
  const res = await gatewayFetch(`${token ? GATEWAY : GATEWAY_HTTP}${URL_PATH}`, {
    headers: { ...(token ? { authorization: `Bearer ${token}` } : {}), ...headers },
  });
  const text = await res.text();
  let body;
  try {
    body = JSON.parse(text);
  } catch {
    body = text;
  }
  return { status: res.status, reason: res.headers.get('x-rid-deny-reason'), body };
}

/** X-RID-* headers the provider (salud mock) logged for one request. */
function providerSaw(requestId) {
  const logs = spawnSync('docker', ['logs', '--since', '2m', 'rid-mock-salud-1'], {
    encoding: 'utf8',
  });
  for (const line of `${logs.stdout}${logs.stderr}`.split('\n')) {
    if (!line.includes(requestId)) continue;
    try {
      const entry = JSON.parse(line);
      if (entry.rid?.['x-request-id'] === requestId) return entry.rid;
    } catch {
      // not a JSON log line
    }
  }
  return undefined;
}


async function preflight() {
  const checks = [
    ['portal API', `${API}/health`],
    ['Keycloak', `${KEYCLOAK}/realms/rid-portal`],
    ['gateway', `${GATEWAY_HTTP}/`],
  ];
  for (const [name, url] of checks) {
    const reachable = await fetch(url).then(
      () => true,
      () => false,
    );
    expect(
      reachable,
      `${name} is not reachable at ${url}. Run \`pnpm infra:up\` and \`pnpm dev\`.`,
    );
  }
}

// --- Kit node (spike) --------------------------------------------------------------------------

const here = dirname(fileURLToPath(import.meta.url));
const ENV_FILE = process.argv[2];
const NODE_CONTAINER = 'rid-kit-spike-kit-node-1';
const OPENAPI_FILE = resolve(here, '../go-node/out/openapi-postgres.json');

function compose(args, extraEnv = {}) {
  const run = spawnSync('docker', ['compose', '--env-file', ENV_FILE, '--profile', 'node', ...args], {
    cwd: here,
    encoding: 'utf8',
    env: { ...process.env, ...extraEnv },
  });
  expect(run.status === 0, `docker compose ${args.join(' ')} failed: ${run.stderr}`);
}

function nodeLogs() {
  const r = spawnSync('docker', ['logs', NODE_CONTAINER], { encoding: 'utf8' });
  return `${r.stdout}${r.stderr}`.split('\n');
}

async function main() {
  expect(ENV_FILE && existsSync(ENV_FILE), 'usage: node kit-e2e.mjs <env-file>');
  console.log(bold('Spike S-2: nodo Go real contra el portal real'));
  await preflight();

  step('Registrar el nodo y emitir el token de enrolamiento', 'elena.admin');
  const elena = await as('elena.admin');
  const salud = elena.entity('salud');
  const issued = await elena.call('POST', `/entities/${salud}/nodes`, { name: `kit-spike-${RUN}` });
  const nodeId = issued.node.id;
  ok(`Nodo ${nodeId} · token de un solo uso, vence ${issued.expiresAt}`);

  step('Arrancar el nodo (contenedor sin capacidades, solo lectura, sin root)', 'entidad');
  // A fresh installation: no previous state (keys, certificate, tunnel) in the node's volume.
  compose(['rm', '-sf', 'kit-node']);
  spawnSync('docker', ['volume', 'rm', '-f', 'rid-kit-spike_kit-node-state']);
  compose(['up', '-d', 'kit-node'], { RID_ENROLLMENT_TOKEN: issued.token });
  const online = await waitFor(
    async () => {
      const n = await elena.call('GET', `/entities/${salud}/nodes/${nodeId}`);
      return n.status === 'online' ? n : undefined;
    },
    { timeoutMs: 120_000, what: 'the node did not come online' },
  );
  ok(`Estado: ${online.status} · túnel ${online.tunnelAddress} · kit ${online.kitVersion}`);
  info(`Servicios declarados: ${JSON.stringify(online.services)}`);
  for (const line of nodeLogs().filter((l) => / node: (enrolled|tunnel up|serving|certificate renewed)/.test(l))) {
    info(`  ${line.replace(/^\S+ /, '')}`);
  }

  // The hub learns the node's SNI route from the portal's reconciler (on enrollment and every
  // minute). Until then the gateway gets a 502: probe the hub without a client certificate until
  // the node itself refuses the handshake, which proves hub → tunnel → node.
  const hubReached = await waitFor(
    async () => {
      spawnSync('docker', ['run', '--rm', '--network', 'rid_providers', '--entrypoint', 'openssl',
        'rid/mock-node:dev', 's_client', '-connect', 'wg-hub:8443', '-servername', `rid-node-${nodeId}`, '-brief'],
        { input: '', encoding: 'utf8', timeout: 20_000, env: { ...process.env, MSYS_NO_PATHCONV: '1' } });
      return nodeLogs().find((l) => l.includes("TLS handshake error from 10.77.0.1") && l.includes("didn't provide a certificate"));
    },
    { timeoutMs: 120_000, everyMs: 5_000, what: 'the hub never reached the node' },
  );
  ok(`El hub real alcanza al nodo por el túnel; sin certificado de cliente el nodo corta: ${hubReached.replace(/^tls: http: /, '')}`);

  step('Publicar la versión con upstream node', 'pedro.publicador');
  const openapi = readFileSync(OPENAPI_FILE, 'utf8');
  const pedro = await as('pedro.publicador');
  const api = await pedro.call('POST', `/entities/${salud}/apis`, {
    slug: SLUG,
    name: `Afiliación desde el kit (spike ${RUN})`,
    summary: 'Consulta de afiliación servida por el nodo Go del spike.',
    description: 'Datos sintéticos.',
    sector: 'salud',
    tags: ['afiliacion'],
    classification: 'personal',
    ownerContact: 'apis@salud.demo.rid.example',
  });
  const version = await pedro.call('POST', `/entities/${salud}/apis/${api.id}/versions`, {
    version: '1.0',
    openapi,
    upstream: { type: 'node', nodeId, servicePath: '/salud-afiliacion' },
    allowedPurposes: ['eligibility_check'],
    exposedFields: [
      { name: 'regimen', personal: true, sensitive: false },
      { name: 'estado', personal: true, sensitive: false },
      { name: 'eps', personal: false, sensitive: false },
    ],
  });
  const declared = online.services?.[0]?.openapiSha256;
  ok(`OpenAPI generado por el nodo aceptado por el lint del portal: ${version.operations.length} operaciones`);
  info(`Hash del OpenAPI en el heartbeat: ${declared}`);
  info(`Hash del archivo subido:        ${createHash('sha256').update(openapi).digest('hex')}`);
  await pedro.call('POST', `/entities/${salud}/apis/${api.id}/versions/${version.id}/submit`);

  step('Validaciones automáticas y aprobación', 'ana.operadora');
  const ana = await as('ana.operadora');
  await waitFor(
    async () =>
      (await ana.call('GET', `/review/api-versions/${version.id}`)).version.validation?.status === 'passed',
    { timeoutMs: 180_000, what: 'the automatic validations did not pass' },
  );
  const review = await ana.call('GET', `/review/api-versions/${version.id}`);
  ok(`Validaciones: ${JSON.stringify(review.version.validation)}`);
  const approved = await ana.call('POST', `/review/api-versions/${version.id}/approve`, {});
  expect(approved.version.status === 'published', 'the version was not published');
  await waitFor(async () => (await callGateway()).status === 401, {
    timeoutMs: 60_000,
    what: 'the APISIX route did not appear',
  });
  ok('Publicada; la ruta existe en el gateway');

  step('Aplicación consumidora, suscripción y aprobación', 'carlos.dev / lucia.datos');
  const carlos = await as('carlos.dev');
  const programas = carlos.entity('programas-sociales');
  const created = await carlos.call('POST', `/entities/${programas}/applications`, {
    name: `Consumidor del spike ${RUN}`,
    description: 'Prueba del nodo Go.',
    environment: 'production',
    technicalContact: 'dev@programas.demo.rid.example',
  });
  await waitFor(
    async () =>
      (await carlos.call('GET', `/entities/${programas}/applications/${created.application.id}`))
        .provisioning === 'ready',
    { timeoutMs: 60_000, what: 'the Keycloak client was not created' },
  );
  if (MTLS) await clientCertificate(carlos, programas, created.application.id, created.application.clientId);
  const token = await gatewayToken(created.application.clientId, created.clientSecret);
  await waitFor(
    async () => {
      const r = await callGateway(token, { 'x-rid-purpose': 'eligibility_check' });
      return r.reason === 'certificate_revoked' ? undefined : r;
    },
    { timeoutMs: 60_000, what: 'the gateway did not learn the new client certificate' },
  );
  const subscription = await carlos.call('POST', `/entities/${programas}/subscriptions`, {
    applicationId: created.application.id,
    apiVersionId: version.id,
    purposeCode: 'eligibility_check',
    purposeDetail: 'Prueba del spike del kit',
    legalBasisText: 'Prueba con datos sintéticos.',
    validityMonths: 12,
  });
  const lucia = await as('lucia.datos');
  await lucia.call('POST', `/entities/${salud}/access-requests/${subscription.id}/approve`, {
    validityMonths: 6,
    comment: 'Spike.',
  });
  await waitFor(
    async () =>
      (await carlos.call('GET', `/entities/${programas}/subscriptions/${subscription.id}`)).status === 'active',
    { timeoutMs: 60_000, what: 'the subscription did not become active' },
  );
  ok('Suscripción activa');

  step('Llamar por el gateway → hub → túnel → nodo Go → PostgreSQL', 'terminal');
  const requestId = `kit-${RUN}`;
  const allowed = await callGateway(token, {
    'x-rid-purpose': 'eligibility_check',
    'x-rid-end-user': 'funcionario-42',
    'x-request-id': requestId,
  });
  expect(allowed.status === 200, `expected 200, got ${allowed.status} ${allowed.reason} ${JSON.stringify(allowed.body)}`);
  ok(`200 · ${JSON.stringify(allowed.body)}`);
  const missing = await gatewayFetch(`${GATEWAY}/salud/${SLUG}/v1/afiliados/9999999999`, {
    headers: {
      authorization: `Bearer ${token}`,
      'x-rid-purpose': 'eligibility_check',
      'x-rid-end-user': 'funcionario-42',
    },
  });
  ok(`Clave inexistente → ${missing.status} ${await missing.text()}`);
  await sleep(1000);
  const seen = nodeLogs().filter((l) => l.startsWith('access:'));
  ok('Lo que registró el nodo (solo metadatos):');
  for (const line of seen.slice(-4)) info(`  ${line}`);
  expect(
    seen.some((l) => l.includes(requestId) && l.includes('"client":"rid-gateway.salud"')),
    'the node did not log the call from the gateway',
  );
  expect(!seen.some((l) => l.includes(PERSON)), 'the node logged the document number');
  ok('El nodo vio al gateway por su nombre y no registró el número de documento');

  const cfg = nodeLogs().filter((l) => l.includes(' node: config 200')).pop();
  info(`Último GET /config visto por el nodo: ${cfg?.replace(/^\S+ /, '')}`);

  console.log(`\n${green(bold('Punta a punta en verde'))} ${dim(`(${((Date.now() - started) / 1000).toFixed(0)} s)`)}`);
}

main().catch((error) => {
  const message = error instanceof DemoError ? error.message : (error.stack ?? String(error));
  console.error(`\n${red(bold(`✗ Falló el paso ${stepNo}`))}: ${message}`);
  process.exit(1);
});

#!/usr/bin/env node
// K1 closing test: the canal image against the REAL portal (../data-exchange). The entity
// registers a node and gets a token; the node enrolls, joins the real WireGuard hub and serves the
// portal's test provider (mock-salud) as an entity's own API; a version is published with
// upstream `node`, passes review, and a consumer calls it through the gateway.
// Helpers (Keycloak login, client certificate, gateway calls) come from the portal's
// scripts/demo.mjs. Prerequisites: `pnpm infra:up` and the portal API on :4000 in ../data-exchange,
// and the image (make image-canal). Usage: node test/e2e/canal-e2e.mjs
import { spawnSync } from 'node:child_process';
import { createHash, randomBytes, randomUUID, X509Certificate } from 'node:crypto';
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { request as httpsRequest } from 'node:https';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { DEFAULT_DEMO_TOTP_SECRET, totp } from '../../../data-exchange/scripts/totp.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../../../data-exchange');
// Against the deployed portal (DEN_E2E_TARGET=vps) every URL and secret comes from the environment.
const VPS = process.env.DEN_E2E_TARGET === 'vps';
if (!VPS && existsSync(resolve(root, '.env'))) process.loadEnvFile(resolve(root, '.env'));

const env = (name, fallback) => process.env[name] ?? fallback;
const API = `${env('DEN_API_URL', 'http://localhost:4000')}/api/v1`;
const KEYCLOAK = env('KEYCLOAK_URL', 'http://localhost:8080');
const GATEWAY = env('DEN_GATEWAY_PUBLIC_URL', 'https://localhost:9443');
// The plain-HTTP listener of the local gateway: only for "does the route exist yet?" (no token).
const GATEWAY_HTTP = env('DEN_GATEWAY_HTTP_URL', 'http://localhost:9080');
// With mTLS (M3-10) the gateway is HTTPS and every call presents the application's certificate.
const MTLS = GATEWAY.startsWith('https://');
const WEB = env('DEN_WEB_URL', 'http://localhost:3000');
const PASSWORD = env('DEN_DEMO_USER_PASSWORD', 'demo');
const TOTP_SECRET = env('DEN_DEMO_TOTP_SECRET', DEFAULT_DEMO_TOTP_SECRET);
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
    // A deployed portal can drop a request now and then: keep waiting instead of failing.
    const value = await probe().catch((error) => {
      if (error instanceof DemoError) throw error;
      return undefined;
    });
    if (value) return value;
    if (Date.now() > until) throw new DemoError(`Timed out after ${timeoutMs / 1000} s: ${what}`);
    await sleep(everyMs);
  }
}

// --- Keycloak login (Authorization Code + PKCE, like the web app) ------------------------------

const PORTAL_CLIENT = {
  realm: 'den-portal',
  clientId: env('DEN_PORTAL_WEB_CLIENT_ID', 'den-portal-web'),
  secret: env('DEN_PORTAL_WEB_CLIENT_SECRET', ''),
  redirectUri: `${WEB}/api/auth/callback/keycloak`,
};
/** The reference citizen folder in the (simulated) national identity (MT-05, MT-07). */
const CITIZEN_CLIENT = {
  realm: 'den-citizens',
  clientId: 'carpeta-ciudadana',
  secret: env('DEN_CARPETA_CLIENT_SECRET', ''),
  redirectUri: `${env('DEN_CARPETA_URL', 'http://localhost:3100')}/auth/callback`,
};

/** Seed users with a critical role: their realm entry has a one-time code (M3-08). */
const MFA_USERS = new Set(
  JSON.parse(readFileSync(resolve(root, 'infra/keycloak/den-portal-realm.json'), 'utf8'))
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
    `Keycloak refused the one-time codes of ${username} (is DEN_DEMO_TOTP_SECRET right?)`,
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
  expect(code, `Login failed for ${username} (is DEN_DEMO_USER_PASSWORD right?)`);
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
  const dir = mkdtempSync(join(tmpdir(), 'den-demo-'));
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
  return { status: res.status, reason: res.headers.get('x-den-deny-reason'), body };
}

/** X-DEN-* headers the provider (salud mock) logged for one request. */
function providerSaw(requestId) {
  const logs = spawnSync('docker', ['logs', '--since', '2m', 'den-mock-salud-1'], {
    encoding: 'utf8',
  });
  for (const line of `${logs.stdout}${logs.stderr}`.split('\n')) {
    if (!line.includes(requestId)) continue;
    try {
      const entry = JSON.parse(line);
      if (entry.den?.['x-request-id'] === requestId) return entry.den;
    } catch {
      // not a JSON log line
    }
  }
  return undefined;
}


async function preflight() {
  const checks = [
    ['portal API', `${API}/health`],
    ['Keycloak', `${KEYCLOAK}/realms/den-portal`],
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

// --- Canal node -------------------------------------------------------------------------------

// The network between this machine and a deployed portal drops a connection now and then:
// retry requests that failed before any answer (TypeError), never ones that got an HTTP status.
const fetchOnce = globalThis.fetch;
globalThis.fetch = async (...args) => {
  for (let attempt = 1; ; attempt += 1) {
    try {
      return await fetchOnce(...args);
    } catch (error) {
      if (!(error instanceof TypeError) || attempt === 3) throw error;
      await sleep(2000 * attempt);
    }
  }
};

const here = dirname(fileURLToPath(import.meta.url));
const COMPOSE_FILE = resolve(here, VPS ? 'compose.vps.yaml' : 'compose.yaml');
const CONFIG_DIR = resolve(here, VPS ? 'config-vps' : 'config');
const PROJECT = VPS ? 'den-kit-e2e-vps' : 'den-kit-e2e';
const NODE_CONTAINER = `${PROJECT}-den-node-1`;
// DEN_E2E_NODE=installer: the node is installed with install.sh from the delivery bundle on a
// clean Ubuntu server (test/install/ubuntu.sh), as an entity would do it.
const INSTALLER = process.env.DEN_E2E_NODE === 'installer';
const KIT = process.env.DEN_E2E_KIT ?? resolve(here, '../../dist/den-node-kit-0.2.0');
// The bundle's own version (its VERSION file): what an install from it must leave in .env.
const KIT_VERSION = existsSync(resolve(KIT, 'VERSION')) ? readFileSync(resolve(KIT, 'VERSION'), 'utf8').trim() : '';
const SERVER = resolve(here, '../install/ubuntu.sh');
const TEST_SERVER = process.env.DEN_TEST_SERVER ?? 'den-install-test';
// DEN_E2E_SERVICE=instant (with DEN_E2E_NODE=installer): the node is installed in modo completo
// and publishes an instant API over a PostgreSQL view of the entity (K3 closing criterion).
const INSTANT = process.env.DEN_E2E_SERVICE === 'instant';
const ROUTE_TEMPLATE = INSTANT ? '/salud/afiliados/{numero_documento}' : '/salud/afiliados/{numeroIdentificacion}';

function onServer(command, extraEnv = {}) {
  return spawnSync('bash', [SERVER, '--exec', command], {
    encoding: 'utf8', env: { ...process.env, MSYS_NO_PATHCONV: '1', ...extraEnv },
  });
}
const PORTAL_OPENAPI = resolve(root, 'infra/mock-providers/salud/openapi.yaml');

function compose(args) {
  const run = spawnSync('docker', ['compose', '-f', COMPOSE_FILE, ...args], {
    cwd: here,
    encoding: 'utf8',
    env: { ...process.env, MSYS_NO_PATHCONV: '1' },
  });
  expect(run.status === 0, `docker compose ${args.join(' ')} failed: ${run.stderr}`);
  return run.stdout;
}

function nodeLogs() {
  const r = INSTALLER ? onServer('docker logs den-node-den-node-1') : spawnSync('docker', ['logs', NODE_CONTAINER], { encoding: 'utf8' });
  return `${r.stdout}${r.stderr}`.split('\n');
}

const events = (msg) =>
  nodeLogs()
    .map((l) => {
      try {
        return JSON.parse(l);
      } catch {
        return undefined;
      }
    })
    .filter((e) => e && e.msg === msg);

// The entity's side with the delivery bundle, on a clean Ubuntu (K2 closing criterion).
async function installWithInstaller(token) {
  step('Instalar con install.sh en un Ubuntu 24.04 limpio (paquete fuera de línea)', 'entidad');
  const portal = API.replace(/\/api\/v1$/, '');
  const first = spawnSync('bash', [SERVER, KIT], {
    encoding: 'utf8',
    env: { ...process.env, MSYS_NO_PATHCONV: '1', DEN_INSTALL_PORTAL: `${portal}/api/v1/`, DEN_INSTALL_TOKEN: 'tok_equivocado_equivocado_000' },
  });
  expect(first.status !== 0, 'an install with a wrong token must not report success');
  ok('Con un token equivocado la instalación no queda en línea y lo dice');
  const retry = onServer(`DEN_INSTALL_YES=1 DEN_INSTALL_TOKEN=' ${token}' bash /root/kit/install.sh </dev/null`);
  info(`${retry.stdout}`.trimEnd().split('\n').slice(-6).join('\n').replace(/^/gm, '  '));
  expect(retry.status === 0, `re-run with the right token failed: ${retry.stdout}${retry.stderr}`);
  ok('Volver a ejecutar con el token correcto enrola el nodo (la URL con /api/v1/ se normalizó)');

  const perms = onServer('stat -c "%n %a %u:%g" /opt/den-node/secrets /opt/den-node/secrets/enrollment_token /opt/den-node/.env /opt/den-node/config/node.yaml /opt/den-node/install.sh').stdout;
  info(perms.trimEnd().replace(/^/gm, '  '));
  for (const want of ['/opt/den-node/secrets 750 0:65532', '/opt/den-node/secrets/enrollment_token 400 65532:65532', '/opt/den-node/.env 600 0:0', 'node.yaml 644', '/opt/den-node/install.sh 755']) {
    expect(perms.includes(want), `permissions: expected "${want}"`);
  }
  ok('Permisos correctos, y el instalador quedó en /opt/den-node/install.sh');

  const op = (cmd) => onServer(`DEN_INSTALL_YES=1 bash /opt/den-node/install.sh ${cmd} </dev/null`);
  const again = op('');
  expect(again.status === 0 && /no token needed/.test(again.stdout), `re-install failed: ${again.stdout}${again.stderr}`);
  ok('Reinstalar no pide token ni toca la identidad del nodo');

  const lan = readFileSync(resolve(here, '../install/.lan-address'), 'utf8').trim();
  spawnSync('docker', ['cp', PORTAL_OPENAPI, `${TEST_SERVER}:/root/salud.openapi.yaml`], { env: { ...process.env, MSYS_NO_PATHCONV: '1' } });
  const svc = (name, path) =>
    onServer(`DEN_INSTALL_SERVICE_NAME=${name} DEN_INSTALL_SERVICE_PATH=${path} DEN_INSTALL_SERVICE_UPSTREAM=http://${lan}:18000 ` +
      `DEN_INSTALL_SERVICE_PLAIN_HTTP=s DEN_INSTALL_SERVICE_CREDENTIAL=ninguna DEN_INSTALL_SERVICE_OPENAPI=/root/salud.openapi.yaml ` +
      `bash /opt/den-node/install.sh service </dev/null`);
  const added = svc('afiliacion', '/salud');
  info(`${added.stdout}`.trimEnd().split('\n').slice(-5).join('\n').replace(/^/gm, '  '));
  expect(added.status === 0, `servicio failed: ${added.stdout}${added.stderr}`);
  ok('Servicio "afiliacion" agregado, validado y aplicado');

  const dup = svc('otra', '/salud');
  expect(dup.status !== 0 && !onServer('ls /opt/den-node/config/services').stdout.includes('otra.yaml') && /repeated/.test(dup.stdout + dup.stderr),
    `a duplicate service must be refused and undone: ${dup.stdout}${dup.stderr}`);
  ok('Un servicio con la ruta repetida se rechaza y no deja archivos');

  expect(svc('temporal', '/temporal').status === 0 && op('service remove temporal').status === 0 &&
    !onServer('ls /opt/den-node/config/services').stdout.includes('temporal'), 'service remove did not remove the service');
  ok('service remove elimina un servicio');

  const bad = op('update 9.9.9');
  const version = onServer("grep '^DEN_NODE_VERSION=' /opt/den-node/.env").stdout.trim();
  expect(bad.status !== 0 && version === `DEN_NODE_VERSION=${KIT_VERSION}` && op('status').status === 0, `update to a missing version: ${bad.stdout}${bad.stderr} / ${version}`);
  ok('update a una versión que no existe falla sin tocar .env y el nodo sigue en línea');

  const name = (op('status').stdout.match(/Node:\s+(\S+)/) ?? [])[1];
  const backup = onServer('bash /opt/den-node/install.sh backup >/dev/null && ls -t /opt/den-node/backups/*.tar.gz | head -1');
  const file = backup.stdout.trim();
  expect(backup.status === 0 && file.endsWith('.tar.gz') && onServer(`stat -c %a ${file}`).stdout.trim() === '600', `backup: ${backup.stdout}${backup.stderr}`);
  onServer('cd /opt/den-node && docker compose down -v >/dev/null 2>&1');
  const restored = op(`restore ${file}`);
  const after = (op('status').stdout.match(/Node:\s+(\S+)/) ?? [])[1];
  expect(restored.status === 0 && after === name, `restore: ${restored.stdout}${restored.stderr} (${name} → ${after})`);
  ok(`Estado perdido y restaurado desde el respaldo: sigue siendo ${after}`);

  for (const mode of ['completo', 'canal']) {
    const m = op(`mode ${mode}`);
    expect(m.status === 0, `mode ${mode} failed: ${m.stdout}${m.stderr}`);
  }
  ok('Cambio de modo completo → canal sin perder el estado');
}

// K3: modo completo and an instant API over a view of the entity's PostgreSQL. Returns the
// OpenAPI the installer generated (the one the publisher uploads).
async function installInstant(token) {
  step('Instalar en modo completo con install.sh en un Ubuntu 24.04 limpio', 'entidad');
  const portal = API.replace(/\/api\/v1$/, '');
  const installed = spawnSync('bash', [SERVER, KIT], {
    encoding: 'utf8',
    env: { ...process.env, MSYS_NO_PATHCONV: '1', DEN_TEST_SERVER: TEST_SERVER, DEN_INSTALL_MODE: 'completo', DEN_INSTALL_PORTAL: portal, DEN_INSTALL_TOKEN: token },
  });
  info(`${installed.stdout}`.trimEnd().split('\n').slice(-4).join('\n').replace(/^/gm, '  '));
  expect(installed.status === 0, `install failed: ${installed.stdout}${installed.stderr}`);
  ok('Nodo instalado en modo completo');

  step('La base de la entidad: PostgreSQL con vistas y un usuario de solo lectura', 'DBA de la entidad');
  const readerPassword = `${randomBytes(12).toString('hex')}@:/?x`; // the DSN must escape it
  const load = spawnSync('bash', ['-c', `docker save postgres:17-alpine | docker exec -i ${TEST_SERVER} docker load -q`], {
    encoding: 'utf8', env: { ...process.env, MSYS_NO_PATHCONV: '1' },
  });
  expect(load.status === 0, `loading postgres into the test server failed: ${load.stderr}`);
  spawnSync('docker', ['cp', resolve(here, '../engines/postgres'), `${TEST_SERVER}:/root/bd-init`], { env: { ...process.env, MSYS_NO_PATHCONV: '1' } });
  // The seed turns TLS on with the throwaway certificates of make engines-up.
  const certs = resolve(here, '../engines/.certs');
  expect(spawnSync('go', ['run', './test/engines/gencerts', certs], { cwd: resolve(here, '../..'), encoding: 'utf8' }).status === 0, 'gencerts failed');
  spawnSync('docker', ['cp', certs, `${TEST_SERVER}:/root/bd-certs`], { env: { ...process.env, MSYS_NO_PATHCONV: '1' } });
  const db = onServer(`docker run -d --name bd-entidad --network den-node_default --restart unless-stopped ` +
    `-e POSTGRES_DB=entidad -e POSTGRES_PASSWORD=${randomBytes(12).toString('hex')} -e DEN_READER_PASSWORD='${readerPassword}' ` +
    `-e DEN_WRITER_PASSWORD=${randomBytes(12).toString('hex')} -v /root/bd-init:/docker-entrypoint-initdb.d:ro -v /root/bd-certs:/certs:ro postgres:17-alpine`);
  expect(db.status === 0, `the entity's database did not start: ${db.stderr}`);
  await waitFor(async () => onServer('docker exec bd-entidad pg_isready -h 127.0.0.1 -U postgres -d entidad').status === 0,
    { timeoutMs: 120_000, everyMs: 2_000, what: "the entity's database is not ready" });
  ok('PostgreSQL bd-entidad con salud.v_afiliados; den_reader solo tiene SELECT sobre las vistas');

  step('Agregar la API instantánea con install.sh service', 'entidad');
  const added = onServer(`DEN_INSTALL_SERVICE_TYPE=instant DEN_INSTALL_SERVICE_NAME=afiliacion DEN_INSTALL_SERVICE_PATH=/salud ` +
    `DEN_INSTALL_SERVICE_ENGINE=postgres DEN_INSTALL_SERVICE_DB_HOST=bd-entidad DEN_INSTALL_SERVICE_DB_PORT=5432 ` +
    `DEN_INSTALL_SERVICE_DB_NAME=entidad DEN_INSTALL_SERVICE_DB_USER=den_reader DEN_INSTALL_SERVICE_DB_PASSWORD='${readerPassword}' ` +
    `DEN_INSTALL_SERVICE_DB_TLS=disable DEN_INSTALL_SERVICE_SOURCE=salud.v_afiliados DEN_INSTALL_SERVICE_KEY=numero_documento ` +
    `DEN_INSTALL_SERVICE_SUBJECT=CC DEN_INSTALL_SERVICE_MANY=n bash /opt/den-node/install.sh service </dev/null`);
  info(`${added.stdout}`.trimEnd().split('\n').slice(-14).join('\n').replace(/^/gm, '  '));
  expect(added.status === 0, `servicio instant failed: ${added.stdout}${added.stderr}`);
  const openapi = onServer('cat /opt/den-node/config/services/afiliacion.openapi.json').stdout;
  const doc = JSON.parse(openapi);
  expect(doc.paths?.['/afiliados/{numero_documento}']?.get?.operationId === 'getAfiliados', `generated OpenAPI: ${openapi.slice(0, 400)}`);
  ok('Servicio instant "afiliacion": catálogo leído, recurso creado y OpenAPI generado (getAfiliados)');
  const yaml = onServer('cat /opt/den-node/config/services/afiliacion.yaml').stdout;
  expect(!yaml.includes(readerPassword), 'the database password ended up in the service file');
  return openapi;
}

async function main() {
  console.log(bold(`${INSTANT ? 'K3: API instantánea (modo completo)' : 'K1: modo canal'} contra el portal ${VPS ? `desplegado (${API})` : 'local'}`));
  await preflight();

  step('Registrar el nodo y emitir el token de enrolamiento', 'elena.admin');
  let elena = await as('elena.admin');
  const salud = elena.entity('salud');
  const issued = await elena.call('POST', `/entities/${salud}/nodes`, { name: `prueba-kit-${INSTANT ? 'instant' : 'canal'}-${RUN}` });
  const nodeId = issued.node.id;
  ok(`Nodo ${nodeId} · token de un solo uso`);

  let instantOpenapi;
  if (INSTANT) {
    expect(INSTALLER, 'DEN_E2E_SERVICE=instant needs DEN_E2E_NODE=installer');
    instantOpenapi = await installInstant(issued.token);
    elena = await as('elena.admin');
  } else if (INSTALLER) {
    await installWithInstaller(issued.token);
    elena = await as('elena.admin'); // the installer phase can outlive the access token
  } else {
  step('Instalar el nodo en modo canal (sin capacidades, solo lectura, sin root, sin puertos)', 'entidad');
  mkdirSync(resolve(here, '.run'), { recursive: true });
  writeFileSync(resolve(here, '.run/enrollment_token'), `${issued.token}\n`, { mode: 0o644 });
  copyFileSync(PORTAL_OPENAPI, resolve(CONFIG_DIR, 'salud.openapi.yaml'));
  compose(['rm', '-sf', 'den-node']);
  spawnSync('docker', ['volume', 'rm', '-f', `${PROJECT}_den-node-state`]);
  compose(['up', '-d', 'den-node']);
  }
  const online = await waitFor(
    async () => {
      const n = await elena.call('GET', `/entities/${salud}/nodes/${nodeId}`);
      return n.status === 'online' ? n : undefined;
    },
    { timeoutMs: 120_000, what: 'the node did not come online' },
  );
  ok(`Estado: ${online.status} · túnel ${online.tunnelAddress} · kit ${online.kitVersion}`);
  info(`Servicios declarados: ${JSON.stringify(online.services)}`);
  if (INSTALLER) {
    // The container was recreated by `servicio` and `modo`: the enrollment line is in an older log.
    const st = onServer('bash /opt/den-node/install.sh status').stdout;
    expect(st.includes(`den-node-${nodeId}`) && /Tunnel:\s+last handshake .* ago/.test(st), `status: ${st}`);
    ok(`Enrolado como den-node-${nodeId}, con handshake reciente del túnel`);
  } else {
    const enrolled = events('enrolled')[0];
    expect(enrolled?.gateway_client === 'den-gateway.salud', 'the node did not log its enrollment');
    ok(`Enrolado como ${enrolled.node}; solo acepta a ${enrolled.gateway_client}`);
  }

  // The hub learns the node's SNI route from the portal's reconciler; until then the gateway
  // gets a 502. Locally, probe the hub without a client certificate until the node itself
  // refuses it; against the VPS the hub is not reachable from here, so the review retries below.
  if (!VPS) {
  const refused = await waitFor(
    async () => {
      spawnSync('docker', ['run', '--rm', '--network', 'den_providers', '--entrypoint', 'openssl', 'den/mock-node:dev',
        's_client', '-connect', 'wg-hub:8443', '-servername', `den-node-${nodeId}`, '-brief'],
        { input: '', encoding: 'utf8', timeout: 20_000, env: { ...process.env, MSYS_NO_PATHCONV: '1' } });
      return events('tls_rejected').find((e) => String(e.from).startsWith('10.77.0.1:'));
    },
    { timeoutMs: 120_000, everyMs: 5_000, what: 'the hub never reached the node' },
  );
  ok(`El hub real alcanza al nodo; sin certificado, el nodo corta: ${refused.reason}`);
  }

  step('Publicar la versión con upstream node', 'pedro.publicador');
  const pedro = await as('pedro.publicador');
  const api = await pedro.call('POST', `/entities/${salud}/apis`, {
    slug: SLUG,
    name: INSTANT ? `Afiliación instantánea (prueba del kit ${RUN})` : `Afiliación por el canal (prueba del kit ${RUN})`,
    summary: INSTANT
      ? 'API instantánea sobre una vista de la base de la entidad, servida por el nodo.'
      : 'API propia de la entidad publicada a través del nodo en modo canal.',
    description: INSTANT ? 'Datos sintéticos (PostgreSQL de prueba de la entidad).' : 'Datos sintéticos (proveedor de prueba del portal).',
    sector: 'salud',
    tags: ['afiliacion'],
    classification: 'personal',
    ownerContact: 'apis@salud.demo.den.example',
  });
  const openapi = INSTANT ? instantOpenapi : readFileSync(PORTAL_OPENAPI, 'utf8');
  const version = await pedro.call('POST', `/entities/${salud}/apis/${api.id}/versions`, {
    version: '1.0',
    openapi,
    upstream: { type: 'node', nodeId, servicePath: '/salud' },
    allowedPurposes: ['eligibility_check'],
    exposedFields: [
      { name: 'regimen', personal: true, sensitive: false },
      { name: 'estado', personal: true, sensitive: false },
      { name: INSTANT ? 'eps' : 'aseguradora', personal: false, sensitive: false },
    ],
  });
  // An instant service declares its OpenAPI's hash once its catalog compiled (a heartbeat later).
  const sha = createHash('sha256').update(openapi).digest('hex');
  await waitFor(
    async () => (await elena.call('GET', `/entities/${salud}/nodes/${nodeId}`)).services?.[0]?.openapiSha256 === sha,
    { timeoutMs: 120_000, everyMs: 5_000, what: 'the heartbeat does not carry the hash of the published OpenAPI' },
  );
  ok('El hash del OpenAPI en el heartbeat coincide con el publicado');
  await pedro.call('POST', `/entities/${salud}/apis/${api.id}/versions/${version.id}/submit`);

  step('Validaciones automáticas y aprobación', 'ana.operadora');
  const ana = await as('ana.operadora');
  // A validation that ran before the hub learned the node's route fails with 502: run it again.
  let reruns = 0;
  await waitFor(
    async () => {
      const status = (await ana.call('GET', `/review/api-versions/${version.id}`)).version.validation?.status;
      if (status === 'failed' && reruns < 6) {
        reruns += 1;
        info(`  validación fallida (¿el hub aún no conoce al nodo?): reintento ${reruns}`);
        await sleep(10_000);
        await pedro.call('POST', `/entities/${salud}/apis/${api.id}/versions/${version.id}/validations`);
      }
      return status === 'passed';
    },
    { timeoutMs: 300_000, everyMs: 3_000, what: 'the automatic validations did not pass' },
  );
  ok(`Validaciones: ${JSON.stringify((await ana.call('GET', `/review/api-versions/${version.id}`)).version.validation.healthcheck)}`);
  const approved = await ana.call('POST', `/review/api-versions/${version.id}/approve`, {});
  expect(approved.version.status === 'published', 'the version was not published');
  await waitFor(async () => (await callGateway()).status === 401, { timeoutMs: 60_000, what: 'the APISIX route did not appear' });
  ok('Publicada; la ruta existe en el gateway');

  step('Aplicación consumidora, suscripción y aprobación', 'carlos.dev / lucia.datos');
  const carlos = await as('carlos.dev');
  const programas = carlos.entity('programas-sociales');
  const created = await carlos.call('POST', `/entities/${programas}/applications`, {
    name: `Consumidor canal ${RUN}`,
    description: 'Prueba del modo canal.',
    environment: 'production',
    technicalContact: 'dev@programas.demo.den.example',
  });
  await waitFor(
    async () => (await carlos.call('GET', `/entities/${programas}/applications/${created.application.id}`)).provisioning === 'ready',
    { timeoutMs: 60_000, what: 'the Keycloak client was not created' },
  );
  if (MTLS) await clientCertificate(carlos, programas, created.application.id, created.application.clientId);
  const token = await gatewayToken(created.application.clientId, created.clientSecret);
  await waitFor(
    async () => {
      const r = await callGateway(token, { 'x-den-purpose': 'eligibility_check' });
      return r.reason === 'certificate_revoked' ? undefined : r;
    },
    { timeoutMs: 60_000, what: 'the gateway did not learn the new client certificate' },
  );
  const subscription = await carlos.call('POST', `/entities/${programas}/subscriptions`, {
    applicationId: created.application.id,
    apiVersionId: version.id,
    purposeCode: 'eligibility_check',
    purposeDetail: 'Prueba del modo canal',
    legalBasisText: 'Prueba con datos sintéticos.',
    validityMonths: 12,
  });
  const lucia = await as('lucia.datos');
  await lucia.call('POST', `/entities/${salud}/access-requests/${subscription.id}/approve`, { validityMonths: 6, comment: 'e2e' });
  await waitFor(
    async () => (await carlos.call('GET', `/entities/${programas}/subscriptions/${subscription.id}`)).status === 'active',
    { timeoutMs: 60_000, what: 'the subscription did not become active' },
  );
  ok('Suscripción activa');

  step(INSTANT ? 'Llamar por el gateway → hub → túnel → nodo (completo) → vista de PostgreSQL'
    : 'Llamar por el gateway → hub → túnel → nodo (canal) → API propia', 'terminal');
  const requestId = randomUUID();
  const allowed = await callGateway(token, {
    'x-den-purpose': 'eligibility_check',
    'x-den-end-user': 'funcionario-42',
    'x-request-id': requestId,
  });
  expect(allowed.status === 200, `expected 200, got ${allowed.status} ${allowed.reason} ${JSON.stringify(allowed.body)}`);
  ok(`200 · ${JSON.stringify(allowed.body)}`);
  if (INSTANT) {
    expect(allowed.body?.numero_documento === PERSON && allowed.body?.eps === 'EPS Dos' && allowed.body?.fecha_afiliacion === '2025-11-25',
      `the instant API did not return the view's row: ${JSON.stringify(allowed.body)}`);
    ok('La respuesta trae los campos de la vista salud.v_afiliados');
  }
  await sleep(1000);
  const access = events('access').find((e) => e.request_id === requestId);
  expect(access, 'the node did not log the call');
  info(`  access: ${JSON.stringify(access)}`);
  expect(access.client === 'den-gateway.salud', 'the node did not see the gateway by name');
  // The gateway's signed assertion (portal ADR-0024): verified where the network signs, absent
  // where it does not yet; never invalid.
  expect(access.assertion === 'verified' || access.assertion === 'absent', `gateway assertion: ${access.assertion}`);
  info(`  aserción del gateway: ${access.assertion}`);
  expect(access.route === ROUTE_TEMPLATE, `route template ${access.route}`);
  expect(!nodeLogs().some((l) => l.includes(PERSON)), 'the node logged the document number');
  ok('El nodo vio al gateway por su nombre, registró la plantilla de la ruta y no el número de documento');

  step('Operación: status y doctor', 'entidad');
  if (INSTALLER) {
    for (const cmd of ['status', 'doctor']) {
      const r = onServer(`bash /opt/den-node/install.sh ${cmd}`);
      info(`${r.stdout}`.trimEnd().replace(/^/gm, '  '));
      expect(r.status === 0, `install.sh ${cmd} exited ${r.status}: ${r.stderr}`);
    }
    ok('install.sh status y doctor sin errores');
  } else {
  info(compose(['exec', '-T', 'den-node', '/den-node', 'status']).trimEnd().replace(/^/gm, '  '));
  const doctor = spawnSync('docker', ['compose', '-f', COMPOSE_FILE, 'exec', '-T', 'den-node', '/den-node', 'doctor'],
    { cwd: here, encoding: 'utf8', env: { ...process.env, MSYS_NO_PATHCONV: '1' } });
  info(`${doctor.stdout}`.trimEnd().replace(/^/gm, '  '));
  expect(doctor.status === 0, `doctor exited ${doctor.status}`);
  ok('doctor sin errores');
  }

  // Cleanup is opt-in: by default the test is left as it finished (node online, version
  // published, subscription active). DEN_E2E_CLEANUP=1 revokes and decommissions.
  if (process.env.DEN_E2E_CLEANUP === '1') {
    step('Limpieza', 'lucia.datos / elena.admin');
    await lucia.call('POST', `/entities/${salud}/access-requests/${subscription.id}/revoke`, { comment: 'Fin de la prueba del kit.' });
    await elena.call('POST', `/entities/${salud}/nodes/${nodeId}/decommission`, { reason: 'Fin de la prueba del kit.' });
    compose(['down', '-v']);
    ok('Suscripción revocada, nodo dado de baja y contenedores locales eliminados');
  } else {
    info(`Queda como terminó: nodo ${nodeId} online (contenedor ${NODE_CONTAINER}), versión publicada y suscripción activa.`);
  }

  console.log(`\n${green(bold('Punta a punta en verde'))} ${dim(`(${((Date.now() - started) / 1000).toFixed(0)} s)`)}`);
}

main().catch((error) => {
  const message = error instanceof DemoError ? error.message : (error.stack ?? String(error));
  console.error(`\n${red(bold(`✗ Falló el paso ${stepNo}`))}: ${message}`);
  process.exit(1);
});

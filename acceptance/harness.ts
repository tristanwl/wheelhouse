import { spawn, type ChildProcess } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, realpathSync, rmSync } from "node:fs";
import net from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

/**
 * Black-box harness: start the server under test as a child process, talk to
 * it over HTTP, capture what it prints, and always kill exactly that process.
 *
 * Nothing here knows what the server is written in. WHEELHOUSE_CMD is any shell
 * command that, with PORT and WHEELHOUSE_LISTEN in its environment, serves
 * Wheelhouse's HTTP API on that port.
 *
 * The server enforces authentication and has no switch to turn it off, so the
 * harness does what a person does: it gives every server its own empty config
 * directory (WHEELHOUSE_CONFIG_DIR and WHEELHOUSE_DB), reads the one-time setup
 * token from `<config dir>/setup-token`, creates the administrator through
 * POST /api/auth/setup, logs in through POST /api/auth/login, and sends the
 * session cookie and CSRF header with every request the scenarios make
 * (`server.request`). `server.anon` sends nothing, for the tests of what an
 * unauthenticated client sees.
 */

export const REPO_ROOT = fileURLToPath(new URL("..", import.meta.url));

/** Run the Go binary built with `make -C server build` unless told otherwise. */
export const DEFAULT_CMD = "./server/wheelhouse";

export const SERVER_CMD = process.env.WHEELHOUSE_CMD || DEFAULT_CMD;

/** Working directory of the server under test (WHEELHOUSE_CWD, default the repository root). */
export const SERVER_CWD = process.env.WHEELHOUSE_CWD || REPO_ROOT;

/** The administrator the harness creates. Obviously fake, and never used anywhere else. */
export const ADMIN = { username: "admin", password: "harness-admin-passphrase-001" };

/** A port nothing listens on right now. */
export function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const { port } = server.address() as net.AddressInfo;
      server.close(() => resolve(port));
    });
  });
}

export interface ApiResponse {
  status: number;
  headers: Headers;
  /** Body as text. */
  text: string;
  /** Body parsed as JSON, or undefined when it is not JSON. */
  json: any;
  /** Status line, headers and body as one string, for scanning. */
  raw: string;
  elapsedMs: number;
}

export type RequestInitEx = RequestInit & { timeoutMs?: number };

async function send(
  baseUrl: string,
  requestPath: string,
  init: RequestInitEx,
  extra: Record<string, string>
): Promise<ApiResponse> {
  const { timeoutMs = 30_000, headers: given, ...rest } = init;
  const headers = new Headers(given);
  for (const [k, v] of Object.entries(extra)) if (!headers.has(k)) headers.set(k, v);
  const started = Date.now();
  const res = await fetch(`${baseUrl}${requestPath}`, {
    ...rest,
    headers,
    redirect: "manual",
    signal: AbortSignal.timeout(timeoutMs),
  });
  const text = await res.text();
  let json: unknown;
  try {
    json = JSON.parse(text);
  } catch {
    json = undefined;
  }
  const lines = [...res.headers].map(([k, v]) => `${k}: ${v}`);
  for (const c of res.headers.getSetCookie()) lines.push(`set-cookie: ${c}`);
  return {
    status: res.status,
    headers: res.headers,
    text,
    json,
    raw: `${res.status}\n${lines.join("\n")}\n\n${text}`,
    elapsedMs: Date.now() - started,
  };
}

/**
 * One HTTP client with its own cookie jar and CSRF token: a browser tab. A
 * fresh Client is anonymous; `login` signs it in. Every request carries the
 * session cookie and, once signed in, the CSRF header (which the server only
 * requires on mutations, and ignores on reads).
 */
export class Client {
  /** Name and value of the session cookie, once signed in. */
  cookie?: { name: string; value: string };
  csrf?: string;
  /** Last login/session body, for tests that need the user or expiry. */
  session?: any;

  constructor(
    readonly baseUrl: string,
    private readonly planted: (secret: string) => void
  ) {}

  request = (requestPath: string, init: RequestInitEx = {}): Promise<ApiResponse> => {
    const extra: Record<string, string> = {};
    if (this.cookie) extra.cookie = `${this.cookie.name}=${this.cookie.value}`;
    if (this.csrf) extra["x-csrf-token"] = this.csrf;
    return send(this.baseUrl, requestPath, init, extra);
  };

  /** POST /api/auth/login; on success remembers the cookie and CSRF token. */
  async login(
    username: string,
    password: string,
    init: { headers?: Record<string, string> } = {}
  ): Promise<ApiResponse> {
    const res = await send(
      this.baseUrl,
      "/api/auth/login",
      {
        method: "POST",
        headers: { "content-type": "application/json", ...init.headers },
        body: JSON.stringify({ username, password }),
      },
      {}
    );
    if (res.status === 200) {
      const set = res.headers.getSetCookie().map((c) => c.split(";")[0]);
      const [name, ...value] = (set[0] ?? "").split("=");
      this.cookie = { name, value: value.join("=") };
      this.csrf = res.json.csrfToken;
      this.session = res.json;
      this.planted(this.cookie.value);
      this.planted(this.csrf!);
    }
    return res;
  }

  /** Forgets the cookie and token without telling the server. */
  forget() {
    this.cookie = undefined;
    this.csrf = undefined;
  }
}

export interface RunningServer {
  baseUrl: string;
  /** The server's config directory: the database, secret.key and the setup token live here. */
  configDir: string;
  dbPath: string;
  /** Everything the server wrote to stdout and stderr so far. */
  output(): string;
  /** Requests as the signed-in administrator (anonymous when started with `auth: "none"`). */
  request(path: string, init?: RequestInitEx): Promise<ApiResponse>;
  /** The administrator's client. */
  admin: Client;
  /** A client that sends nothing: no cookie, no CSRF token. */
  anon: Client;
  /** A new anonymous client (its own cookie jar). */
  newClient(): Client;
  /** The one-time setup token, when the harness read it (it is deleted after setup). */
  setupToken?: string;
  /**
   * Every credential the harness has planted or received on this server: the
   * setup token, passwords, session cookies and CSRF tokens. The secret sweep
   * scans responses, logs and the database for these.
   */
  planted(): string[];
  /** Records another credential for the sweep (a password a test chose). */
  plant(secret: string): void;
  /** Runs `wheelhouse <args>` against the same configuration and waits for it. */
  cli(args: string[]): Promise<{ code: number | null; stdout: string; stderr: string }>;
  /** Kills the child process (and deletes the config directory unless it was given). Safe to call more than once. */
  stop(): Promise<void>;
}

export interface StartOptions {
  /** The service configuration: the same environment variables the app reads today. */
  env?: Record<string, string>;
  readyTimeoutMs?: number;
  /**
   * What the harness does once the server answers /healthz:
   *  - "login" (default): set up the administrator with the setup token and sign in;
   *  - "existing": the config directory already has the administrator (a restart): sign in only;
   *  - "none": nothing, so the test sees a fresh server that needs setup.
   */
  auth?: "login" | "existing" | "none";
  /** Reuse this config directory (a restart). It is not deleted when the server stops. */
  configDir?: string;
  /** Keep the config directory after stop(), so a test can start a second server on it (the test deletes it). */
  keepConfig?: boolean;
}

// The only variables passed through from the test process. Provider variables
// are never inherited, so a developer's own .env or shell cannot leak in.
const PASS_THROUGH = ["PATH", "HOME", "LANG", "LC_ALL", "TMPDIR", "TZ", "SYSTEMROOT"];

const live = new Set<ChildProcess>();

function killAll() {
  for (const child of live) {
    try {
      child.kill("SIGKILL");
    } catch {
      /* already gone */
    }
  }
}
process.on("exit", killAll);

function shellQuote(s: string): string {
  return `'${s.replace(/'/g, `'\\''`)}'`;
}

function serverEnv(
  configDir: string,
  extra: Record<string, string> | undefined,
  port: number
): Record<string, string> {
  const env: Record<string, string> = {};
  for (const name of PASS_THROUGH) {
    const value = process.env[name];
    if (value !== undefined) env[name] = value;
  }
  return Object.assign(
    env,
    {
      WHEELHOUSE_CONFIG_DIR: configDir,
      WHEELHOUSE_DB: path.join(configDir, "wheelhouse.db"),
    },
    extra ?? {},
    {
      PORT: String(port),
      WHEELHOUSE_LISTEN: `127.0.0.1:${port}`,
      // What a container runtime sets. The server must not take its bind address from it.
      HOSTNAME: "0123456789ab",
    }
  );
}

/** Runs `wheelhouse <args>` against a config directory and waits for it (no server involved). */
export async function runCommand(
  args: string[],
  options: { configDir: string; env?: Record<string, string> }
): Promise<{ code: number | null; stdout: string; stderr: string }> {
  const env = serverEnv(options.configDir, options.env, await freePort());
  const cmd = `exec ${SERVER_CMD} ${args.map(shellQuote).join(" ")}`;
  const child = spawn("sh", ["-c", cmd], {
    cwd: SERVER_CWD,
    env: env as unknown as NodeJS.ProcessEnv,
    stdio: ["ignore", "pipe", "pipe"],
  });
  let stdout = "";
  let stderr = "";
  child.stdout!.on("data", (c) => (stdout += c));
  child.stderr!.on("data", (c) => (stderr += c));
  const code = await new Promise<number | null>((resolve) => child.once("exit", resolve));
  return { code, stdout, stderr };
}

interface Spawned {
  child: ChildProcess;
  env: Record<string, string>;
  port: number;
  configDir: string;
  ownsConfigDir: boolean;
  output(): string;
  exited(): string | undefined;
  exit: Promise<void>;
}

async function spawnServer(options: StartOptions): Promise<Spawned> {
  const port = await freePort();
  const ownsConfigDir = options.configDir === undefined;
  // Under the real temp directory: the container wrapper mounts it at the same path.
  const configDir =
    options.configDir ??
    mkdtempSync(path.join(realpathSync(tmpdir()), "wheelhouse-acceptance-cfg-"));
  const env = serverEnv(configDir, options.env, port);

  let output = "";
  // `exec` replaces the shell with the command, so the PID we hold is the
  // server's own and killing it leaves nothing behind.
  const child = spawn("sh", ["-c", `exec ${SERVER_CMD}`], {
    cwd: SERVER_CWD,
    env: env as unknown as NodeJS.ProcessEnv,
    stdio: ["ignore", "pipe", "pipe"],
  });
  live.add(child);
  child.stdout!.on("data", (chunk) => (output += chunk));
  child.stderr!.on("data", (chunk) => (output += chunk));
  let exited: string | undefined;
  const exit = new Promise<void>((resolve) => {
    child.once("exit", (code, signal) => {
      exited = `exited (code ${code}, signal ${signal})`;
      live.delete(child);
      resolve();
    });
  });
  return {
    child,
    env,
    port,
    configDir,
    ownsConfigDir,
    output: () => output,
    exited: () => exited,
    exit,
  };
}

export async function startServer(options: StartOptions = {}): Promise<RunningServer> {
  const sp = await spawnServer(options);
  const baseUrl = `http://127.0.0.1:${sp.port}`;
  const dbPath = path.join(sp.configDir, "wheelhouse.db");
  const secrets = new Set<string>([ADMIN.password]);
  const plant = (s: string) => {
    if (s) secrets.add(s);
  };
  const anon = new Client(baseUrl, plant);
  const admin = new Client(baseUrl, plant);
  const mode = options.auth ?? "login";

  async function stop() {
    if (sp.exited() === undefined) {
      sp.child.kill("SIGTERM");
      const timer = setTimeout(() => sp.child.kill("SIGKILL"), 3000);
      await sp.exit;
      clearTimeout(timer);
    }
    if (sp.ownsConfigDir && !options.keepConfig)
      rmSync(sp.configDir, { recursive: true, force: true });
  }

  // Ready when GET /healthz answers (it needs neither a session nor an account).
  const deadline = Date.now() + (options.readyTimeoutMs ?? 60_000);
  for (;;) {
    if (sp.exited() !== undefined) {
      if (sp.ownsConfigDir) rmSync(sp.configDir, { recursive: true, force: true });
      throw new Error(`server ${sp.exited()} before it was ready\n${sp.output()}`);
    }
    try {
      const res = await anon.request("/healthz", { timeoutMs: 2000 });
      if (res.status === 200) break;
    } catch {
      /* not listening yet */
    }
    if (Date.now() > deadline) {
      await stop();
      throw new Error(`server not ready on /healthz after the timeout\n${sp.output()}`);
    }
    await new Promise((r) => setTimeout(r, 150));
  }

  const server: RunningServer = {
    baseUrl,
    configDir: sp.configDir,
    dbPath,
    output: sp.output,
    request: anon.request,
    admin,
    anon,
    newClient: () => new Client(baseUrl, plant),
    planted: () => [...secrets],
    plant,
    async cli(args) {
      const cmd = `exec ${SERVER_CMD} ${args.map(shellQuote).join(" ")}`;
      const child = spawn("sh", ["-c", cmd], {
        cwd: SERVER_CWD,
        env: sp.env as unknown as NodeJS.ProcessEnv,
        stdio: ["ignore", "pipe", "pipe"],
      });
      let stdout = "";
      let stderr = "";
      child.stdout!.on("data", (c) => (stdout += c));
      child.stderr!.on("data", (c) => (stderr += c));
      const code = await new Promise<number | null>((resolve) => child.once("exit", resolve));
      return { code, stdout, stderr };
    },
    stop,
  };

  try {
    if (mode === "login") {
      const tokenFile = path.join(sp.configDir, "setup-token");
      if (!existsSync(tokenFile)) throw new Error(`no ${tokenFile}\n${sp.output()}`);
      server.setupToken = readFileSync(tokenFile, "utf8").trim();
      plant(server.setupToken);
      const res = await anon.request("/api/auth/setup", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ token: server.setupToken, ...ADMIN }),
      });
      if (res.status !== 201) throw new Error(`setup answered ${res.status}: ${res.text}`);
    }
    if (mode === "login" || mode === "existing") {
      const res = await admin.login(ADMIN.username, ADMIN.password);
      if (res.status !== 200) throw new Error(`login answered ${res.status}: ${res.text}`);
      server.request = admin.request;
    }
  } catch (err) {
    await stop();
    throw err;
  }
  return server;
}

/**
 * Starts a server that is expected to refuse to start, and returns how it
 * ended. `prepare` can damage the config directory first.
 */
export async function startExpectingFailure(
  options: StartOptions & { prepare?: (configDir: string) => void }
): Promise<{ code: string; output: string }> {
  const configDir =
    options.configDir ??
    mkdtempSync(path.join(realpathSync(tmpdir()), "wheelhouse-acceptance-cfg-"));
  options.prepare?.(configDir);
  const sp = await spawnServer({ ...options, configDir });
  try {
    const timer = setTimeout(() => sp.child.kill("SIGKILL"), 30_000);
    await sp.exit;
    clearTimeout(timer);
    return { code: sp.exited() ?? "", output: sp.output() };
  } finally {
    if (options.configDir === undefined) rmSync(configDir, { recursive: true, force: true });
  }
}

/** A URL nothing listens on: connections are refused. */
export async function closedUrl(): Promise<string> {
  return `http://127.0.0.1:${await freePort()}`;
}

/** A server that accepts the connection and then never answers. */
export async function startHangingServer() {
  const sockets = new Set<net.Socket>();
  const server = net.createServer((socket) => {
    sockets.add(socket);
    socket.on("close", () => sockets.delete(socket));
    socket.on("error", () => {});
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const { port } = server.address() as net.AddressInfo;
  return {
    url: `http://127.0.0.1:${port}`,
    close: () =>
      new Promise<void>((resolve) => {
        for (const socket of sockets) socket.destroy();
        server.close(() => resolve());
      }),
  };
}

/** Collects stop functions so a file can clean up everything it started. */
export class Cleanup {
  private readonly stops: (() => Promise<void>)[] = [];
  add<T extends { close?: () => Promise<void>; stop?: () => Promise<void> }>(thing: T): T {
    const stop = thing.stop ?? thing.close;
    if (stop) this.stops.push(() => stop.call(thing));
    return thing;
  }
  async run() {
    await Promise.all(this.stops.splice(0).map((stop) => stop().catch(() => {})));
  }
}

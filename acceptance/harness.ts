import { spawn, type ChildProcess } from "node:child_process";
import net from "node:net";
import { fileURLToPath } from "node:url";

/**
 * Black-box harness: start the server under test as a child process, talk to
 * it over HTTP, capture what it prints, and always kill exactly that process.
 *
 * Nothing here knows what the server is written in. WHEELHOUSE_CMD is any shell
 * command that, with PORT and WHEELHOUSE_LISTEN in its environment, serves
 * Wheelhouse's HTTP API on that port.
 */

export const REPO_ROOT = fileURLToPath(new URL("..", import.meta.url));

/** Run the Go binary built with `make -C server build` unless told otherwise. */
export const DEFAULT_CMD = "./server/wheelhouse";

export const SERVER_CMD = process.env.WHEELHOUSE_CMD || DEFAULT_CMD;

/** Working directory of the server under test (WHEELHOUSE_CWD, default the repository root). */
export const SERVER_CWD = process.env.WHEELHOUSE_CWD || REPO_ROOT;

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

export interface RunningServer {
  baseUrl: string;
  /** Everything the server wrote to stdout and stderr so far. */
  output(): string;
  request(path: string, init?: RequestInit & { timeoutMs?: number }): Promise<ApiResponse>;
  /** Kills the child process. Safe to call more than once. */
  stop(): Promise<void>;
}

export interface StartOptions {
  /** The service configuration: the same environment variables the app reads today. */
  env?: Record<string, string>;
  readyTimeoutMs?: number;
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

export async function startServer(options: StartOptions = {}): Promise<RunningServer> {
  const port = await freePort();
  const env: Record<string, string> = {};
  for (const name of PASS_THROUGH) {
    const value = process.env[name];
    if (value !== undefined) env[name] = value;
  }
  Object.assign(env, options.env ?? {}, {
    PORT: String(port),
    WHEELHOUSE_LISTEN: `127.0.0.1:${port}`,
    // What a container runtime sets. The server must not take its bind address from it.
    HOSTNAME: "0123456789ab",
  });

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

  const baseUrl = `http://127.0.0.1:${port}`;

  async function request(
    path: string,
    init: RequestInit & { timeoutMs?: number } = {}
  ): Promise<ApiResponse> {
    const { timeoutMs = 30_000, ...rest } = init;
    const started = Date.now();
    const res = await fetch(`${baseUrl}${path}`, {
      ...rest,
      signal: AbortSignal.timeout(timeoutMs),
    });
    const text = await res.text();
    let json: unknown;
    try {
      json = JSON.parse(text);
    } catch {
      json = undefined;
    }
    const headers = [...res.headers].map(([k, v]) => `${k}: ${v}`).join("\n");
    return {
      status: res.status,
      headers: res.headers,
      text,
      json,
      raw: `${res.status}\n${headers}\n\n${text}`,
      elapsedMs: Date.now() - started,
    };
  }

  async function stop() {
    if (exited === undefined) {
      child.kill("SIGTERM");
      const timer = setTimeout(() => child.kill("SIGKILL"), 3000);
      await exit;
      clearTimeout(timer);
    }
  }

  // Ready when GET /api/features answers.
  const deadline = Date.now() + (options.readyTimeoutMs ?? 60_000);
  for (;;) {
    if (exited !== undefined) {
      throw new Error(`server ${exited} before it was ready\n${output}`);
    }
    try {
      const res = await request("/api/features", { timeoutMs: 2000 });
      if (res.status === 200) break;
    } catch {
      /* not listening yet */
    }
    if (Date.now() > deadline) {
      await stop();
      throw new Error(`server not ready on /api/features after the timeout\n${output}`);
    }
    await new Promise((r) => setTimeout(r, 150));
  }

  return { baseUrl, output: () => output, request, stop };
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

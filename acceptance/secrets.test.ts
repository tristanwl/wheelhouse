import { createHash } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { startStack, type Mode, type Service, SERVICES, type StackOptions } from "./fakes";
import { ADMIN, startServer, type ApiResponse } from "./harness";
import { ALL_GET_CASES, PAGES } from "./routes";

// Scenario 4: secrets never leave the server. Distinctive canary secrets are
// planted in every provider key, password, token and URL credential; every GET
// route is requested (the table in routes.ts is the list), plus the error
// paths of the mutating routes; no canary may appear in any status line,
// header or body, nor in what the server prints.

/** The forms a secret could take in output: as is, URL-encoded, and base64 (Basic auth). */
function forms(secret: string): string[] {
  return [
    ...new Set([
      secret,
      encodeURIComponent(secret),
      Buffer.from(secret).toString("base64"),
      Buffer.from(secret).toString("base64url"),
    ]),
  ];
}

/** Describes every place `secrets` show up in `haystacks` (name -> text). */
function findLeaks(haystacks: Record<string, string>, secrets: string[]): string[] {
  const leaks: string[] = [];
  for (const [where, text] of Object.entries(haystacks)) {
    for (const secret of secrets) {
      for (const form of forms(secret)) {
        if (text.includes(form)) leaks.push(`${where} contains ${secret}`);
      }
    }
  }
  return leaks;
}

type Probe = {
  name: string;
  run: (
    request: (path: string, init?: RequestInit) => Promise<ApiResponse>
  ) => Promise<ApiResponse>;
};

const json = (body: unknown) => ({
  headers: { "content-type": "application/json" },
  body: JSON.stringify(body),
});

/** Mutating routes, on paths that reach the providers and fail there. */
const MUTATING_PROBES: Probe[] = [
  {
    name: "POST add movie",
    run: (r) =>
      r("/api/add", {
        method: "POST",
        ...json({
          instanceId: "radarr",
          id: 900001,
          qualityProfileId: 1,
          rootFolderPath: "/movies",
          monitored: true,
          searchNow: false,
        }),
      }),
  },
  {
    name: "POST add series",
    run: (r) =>
      r("/api/add", {
        method: "POST",
        ...json({
          instanceId: "sonarr",
          id: 800004,
          qualityProfileId: 1,
          rootFolderPath: "/tv",
          monitored: true,
          searchNow: false,
        }),
      }),
  },
  {
    name: "DELETE movie",
    run: (r) => r("/api/library/radarr/987654?confirm=true", { method: "DELETE" }),
  },
  {
    name: "DELETE season",
    run: (r) => r("/api/library/sonarr/987654/seasons/1?confirm=true", { method: "DELETE" }),
  },
  {
    name: "PUT settings invalid",
    run: (r) => r("/api/codec-analysis/settings", { method: "PUT", ...json({ hd: "x" }) }),
  },
  {
    name: "POST convert invalid",
    run: (r) => r("/api/codec-analysis/convert", { method: "POST", ...json({}) }),
  },
  {
    name: "POST cancel unknown",
    run: (r) => r("/api/codec-analysis/convert/nope/cancel", { method: "POST" }),
  },
];

const WRONG_PASSWORD = "canary-wrong-password-4711";

interface Scenario {
  name: string;
  options: StackOptions;
  /** What must be true of /api/status, so "no leak" is not vacuous. */
  expectStatus: (instances: { id: string; state: string; error?: string }[]) => void;
}

const allIn = (mode: Mode): Partial<Record<Service, Mode>> =>
  Object.fromEntries(SERVICES.map((s) => [s, mode]));

const SCENARIOS: Scenario[] = [
  {
    name: "canary keys rejected by running services",
    options: { canaries: true },
    expectStatus: (instances) => {
      for (const id of ["radarr", "radarr-4k", "sonarr", "sabnzbd", "qbittorrent"]) {
        const i = instances.find((x) => x.id === id)!;
        expect(i.state, id).toBe("down");
        expect(i.error, id).toBeTypeOf("string");
      }
    },
  },
  {
    name: "canary keys with unreachable services",
    options: { canaries: true, modes: allIn("unreachable") },
    expectStatus: (instances) => {
      expect(instances).toHaveLength(7);
      for (const i of instances) expect(i.state, i.id).toBe("down");
    },
  },
  {
    name: "canary credentials embedded in the URLs",
    options: { canaries: true, urlCredentials: true },
    expectStatus: (instances) => {
      for (const i of instances) expect(i.state, i.id).toBe("down");
    },
  },
  {
    name: "canary credentials in URLs of unreachable services",
    options: { canaries: true, urlCredentials: true, modes: allIn("unreachable") },
    expectStatus: (instances) => {
      for (const i of instances) expect(i.state, i.id).toBe("down");
    },
  },
  {
    name: "working keys, healthy services",
    options: {},
    expectStatus: (instances) => {
      expect(instances).toHaveLength(7);
      for (const i of instances) expect(i.state, i.id).not.toBe("down");
    },
  },
];

describe("the scan itself", () => {
  it("notices a secret in any of its forms, and nothing else", () => {
    const secret = "canary-example-secret-1a2b";
    expect(findLeaks({ body: `x ${secret} y` }, [secret])).toHaveLength(1);
    expect(findLeaks({ body: encodeURIComponent("a b") + secret.toUpperCase() }, [secret])).toEqual(
      []
    );
    expect(
      findLeaks({ body: `a=${encodeURIComponent("canary secret/1")}` }, ["canary secret/1"])
    ).toHaveLength(1);
    expect(
      findLeaks({ body: Buffer.from(secret).toString("base64") }, [secret]).length
    ).toBeGreaterThan(0);
    expect(findLeaks({ body: "nothing here" }, [secret])).toEqual([]);
  });
});

describe.each(SCENARIOS)("$name", (scenario) => {
  it("leaks no secret in any GET route, mutating route error, page, header or log", async () => {
    const stack = await startStack(scenario.options);
    const server = await startServer({ env: stack.env });
    try {
      expect(stack.secrets.length).toBeGreaterThanOrEqual(7);

      const haystacks: Record<string, string> = {};
      const crashes: string[] = [];
      const record = async (name: string, run: () => Promise<ApiResponse>) => {
        try {
          const res = await run();
          haystacks[name] = res.raw;
          // A route must answer; a gateway error is fine, a dropped connection is not.
          expect(res.status, name).toBeLessThan(600);
        } catch (err) {
          crashes.push(`${name}: ${err instanceof Error ? err.message : err}`);
        }
      };
      await Promise.all([
        ...ALL_GET_CASES.map((c) => record(`GET ${c.path}`, () => server.request(c.path))),
        ...PAGES.map((p) => record(`GET page ${p.path}`, () => server.request(p.path))),
        ...MUTATING_PROBES.map((p) => record(p.name, () => p.run(server.request))),
      ]);
      expect(crashes).toEqual([]);

      // The scenario must really have happened.
      const status = await server.request("/api/status");
      scenario.expectStatus(status.json.instances);

      expect(findLeaks(haystacks, stack.secrets)).toEqual([]);
      // Whatever the server printed while handling all of that, and while booting.
      expect(findLeaks({ "server output": server.output() }, stack.secrets)).toEqual([]);

      // The credentials of the server's own accounts: the setup token, the
      // administrator's password, the session cookie and the CSRF token (and a
      // wrong password a client tried). None may appear in any response above,
      // in the log, or in the database files. The one exception is the
      // setup-token line the server prints at start so the operator can find it.
      server.plant(WRONG_PASSWORD);
      const failed = await server.anon.request("/api/auth/login", {
        method: "POST",
        ...json({ username: ADMIN.username, password: WRONG_PASSWORD }),
      });
      expect(failed.status).toBe(401);
      const planted = server.planted();
      expect(planted.length).toBeGreaterThanOrEqual(5); // token, password, wrong password, cookie, csrf
      expect(findLeaks({ ...haystacks, "failed login": failed.raw }, planted)).toEqual([]);

      const lines = server.output().split("\n");
      const tokenLines = lines.filter((l) => l.includes(server.setupToken!));
      expect(tokenLines, "the setup token is logged exactly once").toHaveLength(1);
      expect(tokenLines[0]).toContain("setup_token=");
      expect(
        findLeaks(
          { "server output": lines.filter((l) => !l.includes(server.setupToken!)).join("\n") },
          planted
        )
      ).toEqual([]);

      const files: Record<string, string> = {};
      for (const suffix of ["", "-wal", "-shm"]) {
        const f = server.dbPath + suffix;
        if (existsSync(f)) files[`database${suffix || " file"}`] = readFileSync(f, "latin1");
      }
      expect(Object.keys(files)).toContain("database file");
      // The scan is not vacuous: what the database does hold is there, and only as hashes.
      const everything = Object.values(files).join("");
      expect(everything).toContain("$argon2id$");
      const cookieHash = createHash("sha256")
        .update(server.admin.cookie!.value)
        .digest()
        .toString("latin1");
      expect(everything, "the SHA-256 of the session token is stored").toContain(cookieHash);
      expect(findLeaks(files, [...planted, ...stack.secrets])).toEqual([]);
      expect(Object.keys(haystacks).length).toBeGreaterThanOrEqual(
        ALL_GET_CASES.length + PAGES.length + MUTATING_PROBES.length
      );
    } finally {
      await server.stop();
      await stack.close();
    }
  });
});

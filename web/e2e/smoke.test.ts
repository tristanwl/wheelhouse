/**
 * Browser smoke test: the Go binary with the real UI embedded, run against the
 * acceptance suite's fake services, driven by headless Chromium.
 *
 * For every page it asserts: the page renders, the console has no errors, the
 * browser reports no CSP violation, no request failed or answered with an error,
 * and nothing was requested from a host outside the CSP allow-list (so no
 * third-party font, script, style or image loads). It also checks the logo, the
 * self-hosted font and the confirm-then-delete flow against the real server.
 *
 * Needs the binary (built after `npm run build && npm run embed`) and Chromium
 * (`npx playwright-core install chromium`). Without them the tests are skipped
 * with the reason, unless WHEELHOUSE_SMOKE_REQUIRED=1 (CI), where that is a failure.
 */
import { chmodSync, existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium, type Browser, type Page } from "playwright-core";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

const here = dirname(fileURLToPath(import.meta.url));
const repo = resolve(here, "../..");
const binary = process.env.WHEELHOUSE_BIN ?? resolve(repo, "server/wheelhouse");
const required = process.env.WHEELHOUSE_SMOKE_REQUIRED === "1";

// The hosts the CSP lets images come from; the only outside hosts a page may touch.
const IMAGE_HOSTS = ["image.tmdb.org", "artworks.thetvdb.com"];
const PIXEL = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==",
  "base64"
);

async function skipReason(): Promise<string | undefined> {
  if (!existsSync(binary)) {
    return `no server binary at ${binary} (run \`npm run build && npm run embed\` here, then \`make build\` in server/)`;
  }
  try {
    const browser = await chromium.launch();
    await browser.close();
  } catch (err) {
    return `Chromium cannot start (${(err as Error).message.split("\n")[0]}); run \`npx playwright-core install chromium\``;
  }
  return undefined;
}

const reason = await skipReason();
if (reason && required) throw new Error(`smoke test cannot run: ${reason}`);
if (reason) console.warn(`SKIPPED browser smoke test: ${reason}`);

describe.skipIf(reason)("browser smoke test", () => {
  let stack: Awaited<ReturnType<(typeof import("../../acceptance/fakes"))["startStack"]>>;
  let server: Awaited<ReturnType<(typeof import("../../acceptance/harness"))["startServer"]>>;
  let browser: Browser;
  let fakeBin: string;
  const oldPath = process.env.PATH;

  beforeAll(async () => {
    // The Go server runs whatever WHEELHOUSE_CMD says; set it before the harness is loaded.
    process.env.WHEELHOUSE_CMD = binary;
    // Codecs needs ffmpeg on the PATH; a stand-in that only answers `-version` is enough to open the page.
    fakeBin = mkdtempSync(join(tmpdir(), "wheelhouse-smoke-"));
    const ffmpeg = join(fakeBin, "ffmpeg");
    writeFileSync(
      ffmpeg,
      '#!/bin/sh\n[ "$1" = "-version" ] && { echo "ffmpeg version fake"; exit 0; }\nexit 1\n'
    );
    chmodSync(ffmpeg, 0o755);
    process.env.PATH = `${fakeBin}:${oldPath ?? ""}`;

    const { startStack } = await import("../../acceptance/fakes");
    const { startServer } = await import("../../acceptance/harness");
    stack = await startStack();
    server = await startServer({
      env: { ...stack.env, WHEELHOUSE_MEDIA_ROOTS: join(fakeBin, "media") },
    });
    browser = await chromium.launch();
  });

  afterAll(async () => {
    await browser?.close();
    await server?.stop();
    await stack?.close();
    process.env.PATH = oldPath;
    if (fakeBin) rmSync(fakeBin, { recursive: true, force: true });
  });

  interface Visit {
    page: Page;
    problems: string[];
    hosts: Set<string>;
    urls: string[];
    origin: string;
  }

  interface OpenOptions {
    /** Start with the harness administrator's session cookie (default). False: a visitor with no session. */
    session?: boolean;
    /** The server to visit (default: the main one). */
    on?: { baseUrl: string };
    /** HTTP statuses this visit is expected to see (e.g. the 401 of the session probe on the login page). */
    allow?: number[];
  }

  /** Opens a page, collecting everything that should never happen. */
  async function open(path: string, opts: OpenOptions = {}): Promise<Visit> {
    const target = opts.on ?? server;
    const allowed = opts.allow ?? [];
    const context = await browser.newContext({ viewport: { width: 1100, height: 900 } });
    // The harness already set up and signed in the administrator through the API, so
    // the browser can start with that session cookie. The UI gets the CSRF token
    // itself from GET /api/auth/session and sends it, as it does for every person.
    if (opts.session !== false) {
      const session = server.admin.cookie!;
      await context.addCookies([
        {
          name: session.name,
          value: session.value,
          url: server.baseUrl,
          httpOnly: true,
          sameSite: "Strict",
        },
      ]);
    }
    const page = await context.newPage();
    const problems: string[] = [];
    const hosts = new Set<string>();
    const urls: string[] = [];

    // Posters come from real hosts the sandbox cannot reach; serve a pixel for them. The CSP is
    // enforced before this runs, so a host outside the allow-list is still reported as a violation.
    await page.route(
      (url) => IMAGE_HOSTS.includes(url.hostname),
      (route) => route.fulfill({ status: 200, contentType: "image/png", body: PIXEL })
    );
    await page.addInitScript(() => {
      (window as unknown as { __csp: string[] }).__csp = [];
      document.addEventListener("securitypolicyviolation", (e) =>
        (window as unknown as { __csp: string[] }).__csp.push(
          `${e.violatedDirective} blocked ${e.blockedURI || "inline"}`
        )
      );
    });

    page.on("console", (m) => {
      if (m.type() !== "error") return;
      // The browser logs every failed load as a console error; expected statuses are not problems.
      if (allowed.some((st) => m.text().includes(`status of ${st}`))) return;
      problems.push(`console error: ${m.text()}`);
    });
    page.on("pageerror", (e) => problems.push(`page error: ${e.message}`));
    page.on("request", (r) => {
      hosts.add(new URL(r.url()).host);
      urls.push(r.url());
    });
    page.on("requestfailed", (r) => {
      problems.push(`request failed: ${r.url()} (${r.failure()?.errorText})`);
    });
    page.on("response", (r) => {
      if (r.status() >= 400 && !allowed.includes(r.status()))
        problems.push(`HTTP ${r.status()}: ${r.url()}`);
    });

    await page.goto(`${target.baseUrl}${path}`, { waitUntil: "load" });
    const visit: Visit = { page, problems, hosts, urls, origin: new URL(target.baseUrl).host };
    return visit;
  }

  /** Settles, then fails on anything collected, including CSP violations the browser reported. */
  async function expectClean(v: Visit) {
    await v.page.waitForTimeout(400);
    const csp = await v.page.evaluate(() => (window as unknown as { __csp: string[] }).__csp);
    expect(csp, "CSP violations").toEqual([]);
    expect(v.problems).toEqual([]);
    const outside = [...v.hosts].filter((h) => h !== v.origin);
    expect(
      outside.filter((h) => !IMAGE_HOSTS.includes(h)),
      "requests to hosts outside the allow-list"
    ).toEqual([]);
  }

  const pages: { path: string; ready: (p: Page) => Promise<unknown>; name: string }[] = [
    {
      name: "Dashboard",
      path: "/",
      ready: async (p) => {
        await p.getByRole("heading", { name: "Service status" }).waitFor();
        await p.getByRole("heading", { name: "Radarr", exact: true }).waitFor();
        await p.getByText("Example", { exact: false }).first().waitFor();
      },
    },
    {
      name: "Library",
      path: "/library",
      ready: (p) =>
        p
          .getByRole("cell", { name: /Example Movie 1/ })
          .first()
          .waitFor(),
    },
    {
      name: "Library filtered by volume",
      path: "/library?path=%2Fmovies",
      ready: (p) => p.getByRole("heading", { name: /^Library \(/ }).waitFor(),
    },
    {
      name: "Explore",
      path: "/explore",
      ready: async (p) => {
        await p.getByRole("heading", { name: "In Theaters" }).waitFor();
        await p.locator("section img").first().waitFor();
      },
    },
    {
      name: "Explore category",
      path: "/explore/popular",
      ready: async (p) => {
        await p.getByRole("heading", { name: "Popular" }).waitFor();
        await p.locator("img").first().waitFor();
      },
    },
    {
      name: "Codecs",
      path: "/codecs",
      ready: async (p) => {
        await p.getByRole("heading", { name: "Codec Analysis" }).waitFor();
        await p.getByText("Conversion candidates").waitFor();
      },
    },
    {
      name: "unknown path (client 404)",
      path: "/definitely/not/a/page",
      ready: (p) => p.getByRole("heading", { name: "Page not found" }).waitFor(),
    },
  ];

  for (const { name, path, ready } of pages) {
    it(`${name} (${path}): renders with no console errors, CSP violations or failed requests`, async () => {
      const v = await open(path);
      try {
        await ready(v.page);
        await expect(v.page.title()).resolves.toBe("wheelhouse");
        await v.page.getByRole("link", { name: "wheelhouse home" }).waitFor({ state: "visible" });
        await expectClean(v);
      } finally {
        await v.page.context().close();
      }
    });
  }

  it("the page, as served, has no inline script or style and the CSP names only the image hosts", async () => {
    const res = await fetch(`${server.baseUrl}/library`);
    const html = await res.text();
    expect(res.status).toBe(200);
    expect(html).toContain('<div id="root"></div>');
    expect(html).not.toMatch(/<style/i);
    expect(html).not.toMatch(/\sstyle=/i);
    expect(html).not.toMatch(/\son[a-z]+=/i);
    for (const tag of html.match(/<script[^>]*>/gi) ?? []) expect(tag).toContain("src=");

    const csp = res.headers.get("content-security-policy") ?? "";
    expect(csp).toContain(
      "img-src 'self' data: https://image.tmdb.org https://artworks.thetvdb.com;"
    );
    expect(csp).toContain("font-src 'self';");
    expect(csp).not.toContain("unsafe-");
  });

  it("the logo shows at load, winds in after 3.5 s, rolls out on hover, in the self-hosted font", async () => {
    const v = await open("/");
    try {
      const logo = v.page.getByRole("link", { name: "wheelhouse home" });
      await expect.poll(() => logo.getAttribute("data-open")).toBe("true");
      await logo.locator("svg").waitFor({ state: "visible" });
      expect(await logo.textContent()).toContain("wheelhouse");

      await expect.poll(() => logo.getAttribute("data-open"), { timeout: 8000 }).toBe("false");
      await logo.hover();
      await expect.poll(() => logo.getAttribute("data-open")).toBe("true");
      await v.page.mouse.move(600, 600);
      await expect.poll(() => logo.getAttribute("data-open"), { timeout: 4000 }).toBe("false");

      // The wheel really turned (a rotation was applied) and the name is in Bricolage Grotesque.
      const transform = await logo
        .locator("svg")
        .evaluate((el) => (el as SVGElement).style.transform);
      expect(transform).toMatch(/rotate\(-?\d+deg\)/);
      const family = await logo
        .locator(".logo-name > span")
        .evaluate((el) => getComputedStyle(el).fontFamily);
      expect(family).toContain("Bricolage Grotesque");
      const font = v.urls.find((u) => u.endsWith(".woff2"));
      expect(font, "the font is requested").toBeTruthy();
      expect(new URL(font!).host).toBe(new URL(server.baseUrl).host);
      expect(
        await v.page.evaluate(() => document.fonts.check('700 24px "Bricolage Grotesque"'))
      ).toBe(true);
      await expectClean(v);
    } finally {
      await v.page.context().close();
    }
  });

  it("the nav lists the four features the server enables, and a link navigates without a reload", async () => {
    const v = await open("/");
    try {
      const nav = v.page.getByRole("navigation");
      for (const label of ["Dashboard", "Library", "Explore", "Codecs"]) {
        await nav.getByRole("link", { name: label }).waitFor({ state: "visible" });
      }
      await v.page.evaluate(() => ((window as unknown as { __marker: number }).__marker = 1));
      await nav.getByRole("link", { name: "Library" }).click();
      await v.page.waitForURL("**/library");
      await v.page.getByRole("heading", { name: /^Library \(/ }).waitFor();
      expect(
        await v.page.evaluate(() => (window as unknown as { __marker?: number }).__marker)
      ).toBe(1);
      await expectClean(v);
    } finally {
      await v.page.context().close();
    }
  });

  it("the server refuses a library delete without confirm=true, and the UI sends it after its dialog", async () => {
    const radarrDeletes = () =>
      [stack.fakes.radarr, stack.fakes["radarr-4k"]].flatMap(
        (f) => f?.requests.filter((r) => (r as { method: string }).method === "DELETE") ?? []
      );

    // The contract the UI relies on: no confirm, no delete, nothing sent upstream.
    const bare = await server.request("/api/library/radarr/900001", { method: "DELETE" });
    expect(bare.status).toBe(400);
    expect(radarrDeletes()).toHaveLength(0);

    const v = await open("/library");
    try {
      const row = v.page.getByRole("row", { name: /Example Movie 1/ }).first();
      await row.waitFor();
      await row.getByRole("button", { name: "Delete" }).click();

      const dialog = v.page.locator(".fixed", { hasText: /^Delete "Example Movie 1"\?/ });
      await dialog.waitFor();
      // The dialog is up and the server has still heard nothing.
      expect(radarrDeletes()).toHaveLength(0);

      const delRequest = v.page.waitForRequest((r) => r.method() === "DELETE");
      await dialog.getByRole("button", { name: "Delete" }).click();
      const sent = await delRequest;
      expect(new URL(sent.url()).searchParams.get("confirm")).toBe("true");
      await expect.poll(() => radarrDeletes().length).toBe(1);
      await expect.poll(() => v.page.getByRole("row", { name: /Example Movie 1/ }).count()).toBe(1);
      await expectClean(v);
    } finally {
      await v.page.context().close();
    }
  });

  // ---- Authentication, through the UI ----

  const PASSWORD = "smoke-test-passphrase-1";

  async function fillSignIn(p: Page, username: string, password: string) {
    await p.getByLabel("Username").fill(username);
    await p.getByLabel("Password").fill(password);
    await p.getByRole("button", { name: "Sign in" }).click();
  }

  /** Nothing the session needs is readable by page script: no storage, no cookie. */
  async function expectNoScriptReadableSession(p: Page) {
    const seen = await p.evaluate(() => ({
      local: localStorage.length,
      session: sessionStorage.length,
      cookie: document.cookie,
    }));
    expect(seen).toEqual({ local: 0, session: 0, cookie: "" });
  }

  it("a visitor with no session is sent to the login page, signs in, and lands where they were going", async () => {
    const { ADMIN } = await import("../../acceptance/harness");
    const v = await open("/library?path=%2Fmovies", { session: false, allow: [401] });
    try {
      await v.page.getByRole("heading", { name: "Sign in" }).waitFor();
      const url = new URL(v.page.url());
      expect(url.pathname).toBe("/login");
      expect(url.searchParams.get("redirect")).toBe("/library?path=%2Fmovies");
      expect(await v.page.getByLabel("Username").getAttribute("autocomplete")).toBe("username");
      expect(await v.page.getByLabel("Password").getAttribute("autocomplete")).toBe(
        "current-password"
      );
      await fillSignIn(v.page, ADMIN.username, ADMIN.password);
      await v.page.getByRole("heading", { name: /^Library \(/ }).waitFor();
      const landed = new URL(v.page.url());
      expect(landed.pathname + landed.search).toBe("/library?path=%2Fmovies");
      expect(JSON.stringify(v.urls)).not.toContain(ADMIN.password);
      await expectNoScriptReadableSession(v.page);

      // Account page: role, own sessions; the user menu signs out.
      await v.page.getByRole("button", { name: /admin/ }).click();
      await v.page.getByRole("menuitem", { name: "Account" }).click();
      await v.page.getByRole("heading", { name: "Account" }).waitFor();
      await v.page.getByText("This session").waitFor();
      await v.page.getByRole("button", { name: /admin/ }).click();
      await v.page.getByRole("menuitem", { name: "Sign out" }).click();
      await v.page.getByRole("heading", { name: "Sign in" }).waitFor();
      await expectClean(v);
    } finally {
      await v.page.context().close();
    }
  });

  it("first-run setup in the UI: token from the config dir, admin created, signed in, banner, sign-out and a wrong password", async () => {
    const { startServer } = await import("../../acceptance/harness");
    const fresh = await startServer({ auth: "none" });
    try {
      const token = readFileSync(join(fresh.configDir, "setup-token"), "utf8").trim();
      // A fresh server answers 503 "setup required" to the session probe until the wizard is done.
      const v = await open("/", { session: false, on: fresh, allow: [503, 401, 403] });
      try {
        await v.page.getByRole("heading", { name: "Set up Wheelhouse" }).waitFor();
        expect(new URL(v.page.url()).pathname).toBe("/setup");
        await v.page.getByLabel("Setup token").fill(token);
        await v.page.getByLabel("Administrator username").fill("owner");
        await v.page.getByLabel("Password", { exact: true }).fill("short");
        await v.page.getByText(/Too short/).waitFor();
        await v.page.getByLabel("Password", { exact: true }).fill(PASSWORD);
        await v.page.getByLabel("Confirm password").fill(PASSWORD);
        await v.page.getByRole("button", { name: "Create administrator" }).click();

        const banner = v.page.getByRole("status").filter({ hasText: "Add your services" });
        await banner.waitFor();
        expect(await banner.getByRole("link").getAttribute("href")).toBe("/admin");
        expect(new URL(v.page.url()).pathname).toBe("/");
        expect(existsSync(join(fresh.configDir, "setup-token"))).toBe(false);
        await expectNoScriptReadableSession(v.page);

        // /admin exists for the administrator (a placeholder until the admin pages land).
        await v.page.goto(`${fresh.baseUrl}/admin`);
        await v.page.getByRole("heading", { name: "Page not found" }).waitFor();

        await v.page.getByRole("button", { name: /owner/ }).click();
        await v.page.getByRole("menuitem", { name: "Sign out" }).click();
        await v.page.getByRole("heading", { name: "Sign in" }).waitFor();

        await fillSignIn(v.page, "owner", "not-the-right-passphrase");
        const alert = v.page.getByRole("alert");
        await alert.waitFor();
        expect(await alert.textContent()).toMatch(/Invalid username or password/);
        expect(await v.page.getByLabel("Password").inputValue()).toBe("");

        await v.page.waitForTimeout(1500); // the throttle's first delay is short
        await fillSignIn(v.page, "owner", PASSWORD);
        await v.page.getByRole("heading", { name: "Service status" }).waitFor();
        await expectClean(v);
      } finally {
        await v.page.context().close();
      }
    } finally {
      await fresh.stop();
    }
  });

  it("an account with a temporary password must choose a new one first, and a user cannot open /admin", async () => {
    const created = await server.request("/api/users", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        username: "member",
        password: "temporary-passphrase-1",
        role: "user",
      }),
    });
    expect(created.status).toBe(201);
    const v = await open("/codecs", { session: false, allow: [401] });
    try {
      await v.page.getByRole("heading", { name: "Sign in" }).waitFor();
      await fillSignIn(v.page, "member", "temporary-passphrase-1");
      await v.page.getByRole("heading", { name: "Choose a new password" }).waitFor();
      expect(new URL(v.page.url()).pathname).toBe("/change-password");
      await v.page.getByLabel("Current password").fill("temporary-passphrase-1");
      await v.page.getByLabel("New password", { exact: true }).fill(PASSWORD);
      await v.page.getByLabel("Confirm new password").fill(PASSWORD);
      await v.page.getByRole("button", { name: "Change password" }).click();
      await v.page.getByRole("heading", { name: "Codec Analysis" }).waitFor();

      await v.page.goto(`${server.baseUrl}/admin/users`);
      await v.page.getByRole("heading", { name: "Not authorised" }).waitFor();
      await expectClean(v);
    } finally {
      await v.page.context().close();
    }
  });
});

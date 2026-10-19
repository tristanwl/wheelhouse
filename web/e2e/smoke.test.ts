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
    /** The server to visit (default: the main one). Its administrator's cookie is used unless `session` is false. */
    on?: { baseUrl: string; admin?: { cookie?: { name: string; value: string } } };
    /** Browser width in pixels (default 1100); 400 is a phone. */
    width?: number;
    /** HTTP statuses this visit is expected to see (e.g. the 401 of the session probe on the login page). */
    allow?: number[];
  }

  /** Opens a page, collecting everything that should never happen. */
  async function open(path: string, opts: OpenOptions = {}): Promise<Visit> {
    const target = opts.on ?? server;
    const allowed = opts.allow ?? [];
    const context = await browser.newContext({
      viewport: { width: opts.width ?? 1100, height: 900 },
    });
    // The harness already set up and signed in the administrator through the API, so
    // the browser can start with that session cookie. The UI gets the CSRF token
    // itself from GET /api/auth/session and sends it, as it does for every person.
    if (opts.session !== false) {
      const session = (opts.on ?? server).admin!.cookie!;
      await context.addCookies([
        {
          name: session.name,
          value: session.value,
          url: target.baseUrl,
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
        // Every page names itself in the tab ("Library · wheelhouse").
        await expect(v.page.title()).resolves.toMatch(/ · wheelhouse$/);
        await v.page.getByRole("link", { name: "wheelhouse home" }).waitFor({ state: "visible" });
        await expectClean(v);
      } finally {
        await v.page.context().close();
      }
    });
  }

  it("every main and admin page fits a 400px screen without sideways scrolling", async () => {
    const paths = [
      ...pages.map((p) => p.path),
      "/account",
      "/admin/services",
      "/admin/services/new",
      "/admin/features",
      "/admin/users",
      "/admin/sessions",
      "/admin/network",
      "/admin/converter",
      "/admin/audit",
    ];
    const wide: string[] = [];
    for (const path of paths) {
      const v = await open(path, { width: 400 });
      try {
        await v.page.getByRole("link", { name: "wheelhouse home" }).waitFor({ state: "visible" });
        await v.page.waitForLoadState("networkidle");
        const [scroll, inner] = await v.page.evaluate(() => [
          document.documentElement.scrollWidth,
          window.innerWidth,
        ]);
        if (scroll > inner) wide.push(`${path}: ${scroll}px wide in ${inner}px`);
      } finally {
        await v.page.context().close();
      }
    }
    expect(wide).toEqual([]);
  });

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

        const banner = v.page.getByRole("complementary", { name: "Getting started" });
        await banner.waitFor();
        expect(await banner.getByRole("link").getAttribute("href")).toBe("/admin/services/new");
        expect(new URL(v.page.url()).pathname).toBe("/");
        expect(existsSync(join(fresh.configDir, "setup-token"))).toBe(false);
        await expectNoScriptReadableSession(v.page);

        // The administrator can open the admin pages: /admin lands on Services.
        await v.page.goto(`${fresh.baseUrl}/admin`);
        await v.page.getByRole("heading", { name: "Services" }).waitFor();
        expect(new URL(v.page.url()).pathname).toBe("/admin/services");

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

  // ---- The administration pages, end to end ----

  it("an administrator walks through the admin pages against the real server and the fakes", async () => {
    const { startServer } = await import("../../acceptance/harness");
    const { startStack, GOOD } = await import("../../acceptance/fakes");
    const radarrStack = await startStack({ only: ["radarr"] });
    // No service configured: the first-run checklist is what the administrator sees.
    const fresh = await startServer();
    const WRONG_KEY = "wrong-key-for-the-walkthrough";
    const NEW_PASSWORD = "walkthrough-passphrase-2";
    const everything: string[] = [];
    let temporaryPassword: string | undefined;
    try {
      const v = await open("/", { on: fresh });
      v.page.on("response", (r) => {
        if (r.url().includes("/api/"))
          void r.text().then(
            (t) => everything.push(t),
            () => {}
          );
      });
      try {
        const page = v.page;
        const banner = page.getByRole("complementary", { name: "Getting started" });
        await banner.waitFor();
        await banner.getByRole("link", { name: "Add your services" }).click();
        await page.getByRole("heading", { name: "Add a service" }).waitFor();

        // A Radarr that points at the fake: first a wrong key, then the right one.
        await page.getByLabel("Type of service").selectOption("radarr");
        await page.getByLabel(/^URL/).fill(radarrStack.fakes.radarr!.url);
        const key = page.getByLabel(/^API key/);
        expect(await key.getAttribute("type")).toBe("password");
        await key.fill(WRONG_KEY);
        await page.getByRole("button", { name: "Test connection" }).click();
        await page.getByText(/^Connection failed/).waitFor();
        await key.fill(GOOD.arr);
        await page.getByRole("button", { name: "Test connection" }).click();
        await page.getByText(/^Connection works/).waitFor();
        await page.getByRole("button", { name: "Add service" }).click();

        await page.getByText("Radarr was added.").waitFor();
        const row = page.getByRole("listitem", { name: "Radarr" });
        await row.getByText("Source: database").waitFor();
        await row.getByRole("button", { name: "Test connection to Radarr" }).click();
        await row.getByText(/^Connection works/).waitFor();

        // The key is nowhere in the page after saving, nor in any answer the server gave.
        const dom = async () =>
          (await page.content()) +
          (await page.evaluate(() =>
            [...document.querySelectorAll("input")].map((i) => i.value).join(" ")
          ));
        expect(await dom()).not.toContain(GOOD.arr);
        expect(await dom()).not.toContain(WRONG_KEY);
        await row.getByRole("link", { name: "Edit Radarr" }).click();
        await page.getByTestId("secret-state-apiKey").waitFor();
        expect(await dom()).not.toContain(GOOD.arr);
        await page.getByRole("link", { name: "Cancel" }).click();

        // The dashboard shows it, and the checklist is gone.
        await page
          .getByRole("navigation", { name: "Main" })
          .getByRole("link", { name: "Dashboard" })
          .click();
        await page.getByRole("heading", { name: "Radarr", exact: true }).waitFor();
        expect(await banner.count()).toBe(0);

        // Features: switch Library off, the nav loses it; switch it on again.
        await page
          .getByRole("navigation", { name: "Main" })
          .getByRole("link", { name: "Admin" })
          .click();
        await page
          .getByRole("navigation", { name: "Administration" })
          .getByRole("link", { name: "Features" })
          .click();
        const main = page.getByRole("navigation", { name: "Main" });
        await main.getByRole("link", { name: "Library" }).waitFor();
        await page.getByRole("switch", { name: "Library" }).click();
        await page.getByText("Library is now off for everyone.").waitFor();
        await expect.poll(() => main.getByRole("link", { name: "Library" }).count()).toBe(0);
        await page.getByRole("switch", { name: "Library" }).click();
        await page.getByText("Library is now on for everyone.").waitFor();
        await main.getByRole("link", { name: "Library" }).waitFor();

        // Users: create one with a generated password.
        await page
          .getByRole("navigation", { name: "Administration" })
          .getByRole("link", { name: "Users" })
          .click();
        await page.getByRole("button", { name: "Add user" }).click();
        await page.getByLabel("Username").fill("walker");
        await page.getByRole("button", { name: "Generate" }).click();
        const temporary = await page.getByLabel("Password", { exact: true }).inputValue();
        expect(temporary).toHaveLength(20);
        temporaryPassword = temporary;
        await page.getByRole("button", { name: "Create user" }).click();
        const shown = page.getByTestId("one-time-password");
        await shown.waitFor();
        expect(await shown.textContent()).toBe(temporary);
        await page.getByRole("button", { name: "I have passed it on" }).click();
        expect(await page.getByTestId("one-time-password").count()).toBe(0);
        await page.getByRole("row", { name: /walker/ }).waitFor();

        // Network: add a range and save; the page shows where this browser connects from.
        await page
          .getByRole("navigation", { name: "Administration" })
          .getByRole("link", { name: "Network" })
          .click();
        await page.getByRole("button", { name: "Add a range" }).click();
        await page.getByLabel("Local network ranges, entry 1").fill("192.168.1.0/24");
        await page.getByRole("button", { name: "Save network settings" }).click();
        await page.getByText("Network settings saved. They apply right away.").waitFor();
        await page.getByText("127.0.0.1", { exact: true }).waitFor();
        await page.getByText("Outside the ranges above").waitFor();

        // Media and converter: the folders in use and the HDR switch (saving needs real folders).
        await page
          .getByRole("navigation", { name: "Administration" })
          .getByRole("link", { name: "Media and converter" })
          .click();
        await page.getByText(/^In use now:/).waitFor();
        expect(await page.getByRole("switch", { name: /HDR/ }).getAttribute("aria-checked")).toBe(
          "false"
        );

        // Audit log: the above is recorded, in words, and holds no secret.
        await page
          .getByRole("navigation", { name: "Administration" })
          .getByRole("link", { name: "Audit log" })
          .click();
        for (const what of [
          "Service added",
          "Feature switched",
          "User created",
          "Network settings changed",
        ]) {
          await page
            .getByRole("row", { name: new RegExp(what) })
            .first()
            .waitFor();
        }
        const audit = await dom();
        expect(audit).not.toContain(GOOD.arr);
        expect(audit).not.toContain(temporary);

        expect(everything.join("\n")).not.toContain(GOOD.arr);
        expect(everything.join("\n")).not.toContain(WRONG_KEY);
        await expectNoScriptReadableSession(page);
        await expectClean(v);
      } finally {
        await v.page.context().close();
      }

      // The new user signs in, must choose a password, and has no way into the admin pages.
      const w = await open("/", { session: false, on: fresh, allow: [401, 403] });
      try {
        await fillSignIn(w.page, "walker", temporaryPassword!);
        await w.page.getByRole("heading", { name: "Choose a new password" }).waitFor();
        await w.page.getByLabel("Current password").fill(temporaryPassword!);
        await w.page.getByLabel("New password", { exact: true }).fill(NEW_PASSWORD);
        await w.page.getByLabel("Confirm new password").fill(NEW_PASSWORD);
        await w.page.getByRole("button", { name: "Change password" }).click();
        await w.page.getByRole("heading", { name: "Service status" }).waitFor();
        const nav = w.page.getByRole("navigation", { name: "Main" });
        await nav.getByRole("link", { name: "Dashboard" }).waitFor();
        expect(await nav.getByRole("link", { name: "Admin" }).count()).toBe(0);
        await w.page.goto(`${fresh.baseUrl}/admin/services`);
        await w.page.getByRole("heading", { name: "Not authorised" }).waitFor();
        expect(await w.page.getByRole("navigation", { name: "Administration" }).count()).toBe(0);
        // And the server agrees: a user cannot call the admin API.
        const status = await w.page.evaluate(
          async () => (await fetch("/api/admin/instances")).status
        );
        expect(status).toBe(403);
        await expectClean(w);
      } finally {
        await w.page.context().close();
      }
    } finally {
      await fresh.stop();
      await radarrStack.close();
    }
  });
});

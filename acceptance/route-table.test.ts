import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { REPO_ROOT } from "./harness";
import { GET_ROUTES, MUTATING_ROUTES } from "./routes";

// Keeps routes.ts honest while the Next app is the reference: it reads the
// app's route files (it does not import them) and compares them with the
// table, so a new route has to be added there. It is skipped when there is no
// app/api directory, i.e. for a checkout of the port on its own.

const API_DIR = path.join(REPO_ROOT, "app", "api");

function routeFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const full = path.join(dir, name);
    if (statSync(full).isDirectory()) return routeFiles(full);
    return name === "route.ts" ? [full] : [];
  });
}

describe.skipIf(!existsSync(API_DIR))("route table (Next reference app only)", () => {
  const found = existsSync(API_DIR)
    ? routeFiles(API_DIR).flatMap((file) => {
        const pattern =
          "/api/" +
          path
            .relative(API_DIR, path.dirname(file))
            .split(path.sep)
            .filter(Boolean)
            .map((seg) => seg.replace(/^\[(.+)\]$/, "{$1}"))
            .join("/");
        const source = readFileSync(file, "utf8");
        return [
          ...source.matchAll(/export (?:async )?function (GET|POST|PUT|DELETE|PATCH)\b/g),
        ].map((m) => `${m[1]} ${pattern.replace(/\/$/, "")}`);
      })
    : [];

  it("lists exactly the routes the app has", () => {
    const table = [
      ...GET_ROUTES.map((r) => `GET ${r.pattern}`),
      ...MUTATING_ROUTES.map((r) => `${r.method} ${r.pattern}`),
    ];
    expect([...table].sort()).toEqual([...found].sort());
  });

  it("has at least one case per GET route", () => {
    for (const route of GET_ROUTES) expect(route.cases.length, route.pattern).toBeGreaterThan(0);
  });
});

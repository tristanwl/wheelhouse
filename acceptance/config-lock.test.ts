import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { startServer, type RunningServer } from "./harness";

// Issue #83: `rekey` and `forget-secrets` against a running server used to
// succeed and silently make every credential saved afterwards unreadable. The
// server holds a lock on its config directory for its whole life and those
// commands refuse; `backup` stays safe online.

const KEY = "canary-lock-arr-key-3f9a10";
const KEY_AFTER = "canary-lock-arr-key-9e2b44";

const json = (method: string, body: unknown) => ({
  method,
  headers: { "content-type": "application/json" },
  body: JSON.stringify(body),
});

const instance = (key: string) => ({
  providerId: "radarr",
  name: "Radarr",
  config: { url: "http://192.0.2.10:7878", apiKey: key },
});

async function secretIsSet(server: RunningServer, id: string): Promise<boolean> {
  const res = await server.request("/api/admin/instances");
  const found = (
    res.json.instances as { id: string; secrets: { apiKey?: { set: boolean } }; status: string }[]
  ).find((i) => i.id === id);
  return found?.secrets.apiKey?.set === true && found.status === "ready";
}

describe("the configuration lock", () => {
  it("refuses rekey and forget-secrets while the server runs, and allows them once it stopped", async () => {
    const server = await startServer({ keepConfig: true });
    const dir = server.configDir;
    server.plant(KEY);
    server.plant(KEY_AFTER);
    const scratch = mkdtempSync(path.join(tmpdir(), "wheelhouse-lock-"));
    try {
      expect(
        (await server.request("/api/admin/instances", json("POST", instance(KEY)))).status
      ).toBe(201);

      for (const args of [["rekey"], ["forget-secrets", "--yes"]]) {
        const res = await server.cli(args);
        expect(res.code, `${args[0]}: ${res.stdout}`).not.toBe(0);
        expect(res.stderr).toMatch(/refusing to run/);
        expect(res.stderr).toMatch(/server is running/);
        expect(res.stderr).toMatch(/throwaway container/);
        expect(res.stdout).not.toMatch(/Re-encrypted|Discarded/);
      }

      // Nothing was touched: the stored key still opens, and a key saved now is
      // still readable (before the lock existed it was lost).
      expect(await secretIsSet(server, "radarr")).toBe(true);
      const second = await server.request(
        "/api/admin/instances",
        json("POST", { ...instance(KEY_AFTER), id: "radarr-4k", name: "Radarr 4K" })
      );
      expect(second.status, second.text).toBe(201);

      // A backup is safe online, with the lock held.
      const backup = await server.cli(["backup", path.join(scratch, "online.db")]);
      expect(backup.code, backup.stderr).toBe(0);
      expect(server.output()).not.toContain(KEY);

      // The server is still serving after the refusals.
      expect((await server.request("/api/admin/instances")).status).toBe(200);

      await server.stop();
      const rekey = await server.cli(["rekey"]);
      expect(rekey.code, rekey.stderr).toBe(0);
      expect(rekey.stdout).toMatch(/Re-encrypted 2 secret/);

      // And the restarted server reads both keys with the new key.
      const again = await startServer({ configDir: dir, auth: "existing" });
      try {
        expect(await secretIsSet(again, "radarr")).toBe(true);
        expect(await secretIsSet(again, "radarr-4k")).toBe(true);
      } finally {
        await again.stop();
      }
    } finally {
      await server.stop();
      rmSync(dir, { recursive: true, force: true });
      rmSync(scratch, { recursive: true, force: true });
    }
  });

  it("does not start a second server on the same configuration directory", async () => {
    const first = await startServer({ keepConfig: true });
    try {
      await expect(startServer({ configDir: first.configDir, auth: "none" })).rejects.toThrow();
    } finally {
      await first.stop();
      rmSync(first.configDir, { recursive: true, force: true });
    }
  });
});

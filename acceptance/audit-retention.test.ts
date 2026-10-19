import { describe, expect, it } from "vitest";
import { startServer } from "./harness";

// Issue #84: audit retention is configuration (read-only, from the environment),
// shown to administrators on GET /api/audit, and an invalid value refuses to start.

describe("audit retention", () => {
  it("defaults to 180 days with a 30-day floor, and is shown read-only to administrators", async () => {
    const server = await startServer();
    try {
      const res = await server.request("/api/audit");
      expect(res.status).toBe(200);
      expect(res.json.retention).toMatchObject({ days: 180, floorDays: 30 });
      // The audit log cannot be changed through the API: no write route exists.
      for (const method of ["PUT", "PATCH", "DELETE", "POST"]) {
        const w = await server.request("/api/audit", { method });
        expect([404, 405], `${method} /api/audit`).toContain(w.status);
      }
      expect((await server.request("/api/audit")).json.entries.length).toBeGreaterThan(0);
    } finally {
      await server.stop();
    }
  });

  it("takes WHEELHOUSE_AUDIT_RETENTION_DAYS", async () => {
    const server = await startServer({ env: { WHEELHOUSE_AUDIT_RETENTION_DAYS: "365" } });
    try {
      const res = await server.request("/api/audit");
      expect(res.json.retention).toMatchObject({ days: 365, floorDays: 30 });
    } finally {
      await server.stop();
    }
  });

  it.each(["29", "3651", "forever", "0"])(
    "refuses to start with an invalid value (%s)",
    async (value) => {
      await expect(
        startServer({ env: { WHEELHOUSE_AUDIT_RETENTION_DAYS: value }, auth: "none" })
      ).rejects.toThrow(
        /WHEELHOUSE_AUDIT_RETENTION_DAYS must be a whole number of days from 30 to 3650/
      );
    }
  );
});

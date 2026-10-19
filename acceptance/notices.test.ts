import { describe, expect, it } from "vitest";
import { startServer } from "./harness";

// Issue #85: what people read never names a setting by its key ("apiKey") or
// shows the "key: message" form; it uses the label the admin form shows.

const KEYS = /apiKey|baseUrl|\bapikey\b|[a-z]+: (is required|must )/;

const json = (method: string, body: unknown) => ({
  method,
  headers: { "content-type": "application/json" },
  body: JSON.stringify(body),
});

describe("notices name settings by their label", () => {
  it("a service defined by environment variables with a URL but no key", async () => {
    const server = await startServer({
      env: { RADARR_URL: "http://192.0.2.10:7878", SABNZBD_URL: "http://192.0.2.11:8080" },
    });
    try {
      const status = await server.request("/api/status");
      expect(status.status).toBe(200);
      const notices = (status.json.instances as { id: string; error?: string }[]).map(
        (i) => `${i.id}: ${i.error ?? ""}`
      );
      expect(notices.join("\n")).toContain("radarr: API key is required");
      expect(notices.join("\n")).toContain("sabnzbd: API key is required");
      expect(status.text).not.toMatch(KEYS);

      // The admin page says the same, and the form's field map still has the key.
      const list = await server.request("/api/admin/instances");
      const radarr = (list.json.instances as { id: string; error?: string }[]).find(
        (i) => i.id === "radarr"
      );
      expect(radarr?.error).toBe("API key is required");
      for (const i of list.json.instances as { error?: string }[])
        expect(i.error ?? "").not.toMatch(KEYS);
    } finally {
      await server.stop();
    }
  });

  it("form errors map to the field key and read as sentences", async () => {
    const server = await startServer();
    try {
      const res = await server.request(
        "/api/admin/instances",
        json("POST", { providerId: "radarr", name: "Radarr", config: { url: "ftp://nope" } })
      );
      expect(res.status).toBe(400);
      const fields = res.json.fields as { field: string; message: string }[];
      expect(fields).toEqual([
        { field: "url", message: "URL must be an http or https URL" },
        { field: "apiKey", message: "API key is required" },
      ]);
      expect(res.text.replace(/"field":"[a-zA-Z]+"/g, "")).not.toMatch(KEYS);

      // The same settings through the connection test.
      const test = await server.request(
        "/api/admin/instances/test",
        json("POST", { providerId: "radarr", config: { url: "http://192.0.2.10:7878" } })
      );
      expect(test.status).toBe(400);
      expect(test.json.fields).toEqual([{ field: "apiKey", message: "API key is required" }]);
    } finally {
      await server.stop();
    }
  });
});

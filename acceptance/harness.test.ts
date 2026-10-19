import http from "node:http";
import type net from "node:net";
import { describe, expect, it } from "vitest";
import { freePort, startServer } from "./harness";

// The harness picks a port that was free a moment ago. If another worker (or a
// fake service) takes it before the server binds, that other server answers
// /healthz, and the harness used to take it for its own, then found no setup
// token in its own config directory ("no <cfg>/setup-token", seen once in CI).
describe("starting a server", () => {
  it("is not fooled by another server answering /healthz on its port", async () => {
    const port = await freePort();
    const decoy = http.createServer((_req, res) => {
      res.writeHead(200, { "content-type": "application/json" });
      res.end("{}");
    });
    await new Promise<void>((resolve) => decoy.listen(port, "127.0.0.1", resolve));
    try {
      const server = await startServer({ firstPort: port });
      try {
        // Signed in to its own server, which is not the decoy's port.
        expect(server.baseUrl).not.toBe(`http://127.0.0.1:${port}`);
        expect((await server.request("/api/status")).status).toBe(200);
        expect(server.output()).toContain("listening");
      } finally {
        await server.stop();
      }
    } finally {
      await new Promise((resolve) => (decoy as net.Server).close(resolve));
    }
  });
});

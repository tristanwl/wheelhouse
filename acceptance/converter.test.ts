import {
  chmodSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  realpathSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { SERVER_CMD, startServer, type RunningServer } from "./harness";

// Converter settings (media roots, HDR switch) and the persisted job queue
// (Phase 2 item 8, issues #31 to #33), against the real server.
//
// The scenarios that need a fake ffmpeg put it first on the server's PATH, which
// a container cannot take, so they are skipped when the suite runs against the
// image (the real ffmpeg is there; the Go tests cover the same paths with fakes).

const inContainer = SERVER_CMD.includes("docker");
const json = (value: unknown) => ({
  headers: { "content-type": "application/json" },
  body: JSON.stringify(value),
});
const scratch = () => mkdtempSync(path.join(realpathSync(tmpdir()), "wheelhouse-acceptance-conv-"));

let dirs: string[] = [];
afterAll(() => {
  for (const d of dirs) rmSync(d, { recursive: true, force: true });
});
const tmp = () => {
  const d = scratch();
  dirs.push(d);
  return d;
};

describe("GET and PUT /api/admin/converter", () => {
  let server: RunningServer;
  let root: string;
  beforeAll(async () => {
    root = tmp();
    server = await startServer({ env: { WHEELHOUSE_MEDIA_ROOTS: root } });
  });
  afterAll(async () => {
    await server?.stop();
  });
  const put = (body: unknown) =>
    server.request("/api/admin/converter", { method: "PUT", ...json(body) });

  it("shows the environment roots until the database has some", async () => {
    const res = await server.request("/api/admin/converter");
    expect(res.status).toBe(200);
    expect(res.json).toEqual({
      mediaRoots: [root],
      includeHdr: false,
      effectiveRoots: [root],
      source: "environment",
    });
  });

  it("refuses roots that are unsafe, missing, nested or not folders", async () => {
    const file = path.join(root, "f.mkv");
    writeFileSync(file, "x");
    const sub = path.join(root, "sub");
    mkdirSync(sub);
    for (const bad of [
      ["/"],
      ["/etc"],
      ["/proc"],
      ["/usr/share"],
      ["relative/movies"],
      [`${root}/../etc`],
      [path.join(root, "nope")],
      [file],
      [root, sub],
      [""],
    ]) {
      const res = await put({ mediaRoots: bad });
      expect(res.status, JSON.stringify(bad)).toBe(400);
      expect(res.json.error).toBeTypeOf("string");
    }
    expect((await put({})).status).toBe(400);
    expect((await put({ includeHdr: "yes" })).status).toBe(400);
    expect((await server.request("/api/admin/converter")).json.source).toBe("environment");
  });

  it("stores roots and the HDR switch, and the database wins", async () => {
    const other = tmp();
    const res = await put({ mediaRoots: [other], includeHdr: true });
    expect(res.status, res.text).toBe(200);
    expect(res.json).toEqual({
      mediaRoots: [other],
      includeHdr: true,
      effectiveRoots: [other],
      source: "database",
    });
    // A conversion request outside the new roots is refused at once (no restart).
    const outside = path.join(root, "Example Movie 1 (2025).mkv");
    writeFileSync(outside, "x");
    const refused = await server.request("/api/codec-analysis/convert", {
      method: "POST",
      ...json({
        instanceId: "radarr",
        itemId: 1,
        filePath: outside,
        title: "Example Movie 1",
        originalSize: 1,
        backend: "software",
        targetFamily: "hevc",
      }),
    });
    expect(refused.status).toBe(400);
    // The change is in the audit log.
    const audit = await server.request("/api/audit?limit=50");
    const entry = (audit.json.entries as { action: string }[]).find(
      (e) => e.action === "converter.update"
    );
    expect(entry).toBeDefined();
  });

  it("an empty list removes the saved roots again", async () => {
    const res = await put({ mediaRoots: [] });
    expect(res.status).toBe(200);
    expect(res.json.source).toBe("environment");
    expect(res.json.effectiveRoots).toEqual([root]);
    expect(res.json.includeHdr).toBe(true);
    await put({ includeHdr: false });
  });
});

describe("the settings and the queue survive a restart", () => {
  it("keeps database roots over the environment, and finished jobs with their owner", async () => {
    const root = tmp();
    const other = tmp();
    const env = { WHEELHOUSE_MEDIA_ROOTS: root };
    const first = await startServer({ env, keepConfig: true });
    const dir = first.configDir;
    try {
      expect(
        (
          await first.request("/api/admin/converter", {
            method: "PUT",
            ...json({ mediaRoots: [root, other], includeHdr: true }),
          })
        ).status
      ).toBe(200);
      // A job that fails at once (the file is not a video), acknowledged as HDR
      // because the server cannot classify it.
      const file = path.join(other, "Example Movie 1 (2025).mkv");
      writeFileSync(file, "not a real video");
      const start = await first.request("/api/codec-analysis/convert", {
        method: "POST",
        ...json({
          instanceId: "radarr",
          itemId: 1,
          filePath: file,
          title: "Example Movie 1",
          originalSize: 16,
          backend: "software",
          targetFamily: "hevc",
          acknowledgeHdr: true,
        }),
      });
      expect(start.status, start.text).toBe(200);
      const id = start.json.id as string;
      await waitFor(
        async () =>
          (await first.request(`/api/codec-analysis/convert/${id}`)).json.status === "failed"
      );
      await first.stop();

      const second = await startServer({ env, configDir: dir, auth: "existing" });
      try {
        const cfg = (await second.request("/api/admin/converter")).json;
        expect(cfg.source).toBe("database");
        expect(cfg.effectiveRoots).toEqual([root, other]);
        expect(cfg.includeHdr).toBe(true);
        const job = (await second.request(`/api/codec-analysis/convert/${id}`)).json;
        expect(job.status).toBe("failed");
        expect(job.ownerName).toBe("admin");
        expect(job.owner).toBeTypeOf("string");
        expect(job.acknowledgedHdr).toBe(true);
        expect(
          (await second.request("/api/codec-analysis/convert")).json.jobs.map(
            (j: { id: string }) => j.id
          )
        ).toContain(id);
        const audit = (await second.request("/api/audit?limit=100")).json.entries as {
          action: string;
          target: string;
        }[];
        expect(audit.some((e) => e.action === "conversion.start" && e.target === id)).toBe(true);
        expect(audit.some((e) => e.action === "conversion.finish" && e.target === id)).toBe(true);
      } finally {
        await second.stop();
      }
    } finally {
      await first.stop();
      rmSync(dir, { recursive: true, force: true });
    }
  });
});

async function waitFor(cond: () => Promise<boolean>, what = "condition", ms = 30_000) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    if (await cond()) return;
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error(`timed out waiting for ${what}`);
}

/** A fake ffmpeg and ffprobe, steered by files next to them. */
function fakeTools() {
  const dir = tmp();
  const script = (name: string, body: string) => {
    writeFileSync(path.join(dir, name), `#!/bin/sh\nhere=$(dirname "$0")\n${body}\n`);
    chmodSync(path.join(dir, name), 0o755);
  };
  script(
    "ffmpeg",
    `out=""; for a in "$@"; do out="$a"; done
case "$(cat "$here/mode")" in
  slow) printf partial > "$out"; echo out_time_ms=1000000; exec sleep 30;;
  *) echo out_time_ms=1000000; printf converted > "$out"; echo progress=end;;
esac`
  );
  script("ffprobe", `case "$*" in *stream=*) cat "$here/stream.json";; *) echo 2.0;; esac`);
  const set = (mode: "ok" | "slow", stream = '{"streams":[{"color_transfer":"bt709"}]}') => {
    writeFileSync(path.join(dir, "mode"), mode);
    writeFileSync(path.join(dir, "stream.json"), stream);
  };
  set("ok");
  return { dir, set, env: { PATH: `${dir}:${process.env.PATH ?? ""}` } };
}

const body = (file: string, extra: Record<string, unknown> = {}) =>
  json({
    instanceId: "radarr",
    itemId: 1,
    filePath: file,
    title: "Example Movie 1",
    originalSize: 8,
    backend: "software",
    targetFamily: "hevc",
    ...extra,
  });

describe.skipIf(inContainer)("HDR and Dolby Vision (fake ffmpeg)", () => {
  it("excludes HDR by default, asks for an acknowledgement when allowed, and records it", async () => {
    const root = tmp();
    const tools = fakeTools();
    const server = await startServer({ env: { WHEELHOUSE_MEDIA_ROOTS: root, ...tools.env } });
    try {
      const file = path.join(root, "Example Movie 1 (2025).mkv");
      writeFileSync(file, "original");
      tools.set("ok", '{"streams":[{"color_transfer":"smpte2084","color_primaries":"bt2020"}]}');

      const excluded = await server.request("/api/codec-analysis/convert", {
        method: "POST",
        ...body(file, { acknowledgeHdr: true }),
      });
      expect(excluded.status, excluded.text).toBe(409);
      expect(excluded.json).toMatchObject({ code: "hdr-excluded", hdr: "hdr10" });

      expect(
        (
          await server.request("/api/admin/converter", {
            method: "PUT",
            ...json({ includeHdr: true }),
          })
        ).status
      ).toBe(200);
      const asked = await server.request("/api/codec-analysis/convert", {
        method: "POST",
        ...body(file),
      });
      expect(asked.status).toBe(409);
      expect(asked.json).toMatchObject({ code: "hdr", hdr: "hdr10" });
      expect(asked.json.error).toMatch(/SDR 8-bit/);
      expect((await server.request("/api/codec-analysis/convert")).json).toEqual({ jobs: [] });

      const ok = await server.request("/api/codec-analysis/convert", {
        method: "POST",
        ...body(file, { acknowledgeHdr: true }),
      });
      expect(ok.status, ok.text).toBe(200);
      expect(ok.json).toMatchObject({ hdr: "hdr10", acknowledgedHdr: true });
      await waitFor(
        async () =>
          (await server.request(`/api/codec-analysis/convert/${ok.json.id}`)).json.status ===
          "success"
      );
      expect(readFileSync(file, "utf8")).toBe("converted");
    } finally {
      await server.stop();
    }
  });
});

describe.skipIf(inContainer)("a restart mid-conversion (fake ffmpeg)", () => {
  it("fails the running job as interrupted, cleans up, and resumes queued jobs in order", async () => {
    const root = tmp();
    const tools = fakeTools();
    const env = { WHEELHOUSE_MEDIA_ROOTS: root, ...tools.env };
    const fileA = path.join(root, "A (2025).mkv");
    const fileB = path.join(root, "B (2025).mkv");
    const fileC = path.join(root, "C (2025).mkv");
    for (const f of [fileA, fileB, fileC]) writeFileSync(f, "original");

    tools.set("slow");
    const first = await startServer({ env, keepConfig: true });
    const dir = first.configDir;
    try {
      const ids: string[] = [];
      for (const f of [fileA, fileB, fileC]) {
        const res = await first.request("/api/codec-analysis/convert", {
          method: "POST",
          ...body(f),
        });
        expect(res.status, res.text).toBe(200);
        ids.push(res.json.id);
      }
      await waitFor(
        async () =>
          (await first.request(`/api/codec-analysis/convert/${ids[0]}`)).json.status === "running"
      );
      // The slow fake wrote partial output next to the original.
      // ("running" is reported a moment before the fake's first write lands.)
      await waitFor(
        async () => readdirSync(root).some((n) => n.startsWith(".wheelhouse-convert-")),
        "partial output"
      );
      await first.stop();

      tools.set("ok");
      const second = await startServer({ env, configDir: dir, auth: "existing" });
      try {
        const get = async (id: string) =>
          (await second.request(`/api/codec-analysis/convert/${id}`)).json;
        const a = await get(ids[0]);
        expect(a.status).toBe("failed");
        expect(a.error).toMatch(/interrupted/);
        await waitFor(
          async () =>
            (await get(ids[1])).status === "success" && (await get(ids[2])).status === "success"
        );
        const [b, c] = [await get(ids[1]), await get(ids[2])];
        expect(b.startedAt).toBeLessThanOrEqual(c.startedAt);
        expect(readFileSync(fileA, "utf8")).toBe("original");
        expect(readFileSync(fileB, "utf8")).toBe("converted");
        expect(readFileSync(fileC, "utf8")).toBe("converted");
        expect(existsSync(`${fileA}.bak`)).toBe(false);
        expect(readdirSync(root).filter((n) => n.startsWith(".wheelhouse-convert-"))).toEqual([]);
        // The list keeps the order the jobs were added in.
        const list = (await second.request("/api/codec-analysis/convert")).json.jobs as {
          id: string;
        }[];
        expect(list.map((j) => j.id)).toEqual(ids);
        const audit = (await second.request("/api/audit?limit=100")).json.entries as {
          action: string;
          target: string;
        }[];
        expect(
          audit.filter((e) => e.action === "conversion.finish" && e.target === ids[0])
        ).toHaveLength(1);
        expect(audit.filter((e) => e.action === "conversion.start")).toHaveLength(3);
      } finally {
        await second.stop();
      }
    } finally {
      await first.stop();
      rmSync(dir, { recursive: true, force: true });
    }
  });
});

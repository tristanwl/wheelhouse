import { link, mkdtemp, rm, unlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// The queue would start ffmpeg; the route's job here is only to decide whether
// to hand it a file.
const queue = vi.hoisted(() => ({
  enqueueConversion: vi.fn((input: { filePath: string }) => ({ id: "job-1", ...input })),
  findActiveJobForPath: vi.fn((): { id: string } | undefined => undefined),
  listJobs: vi.fn(() => []),
}));
vi.mock("@/lib/convert-queue", () => queue);

import { linkCount } from "@/lib/hardlink";
import { POST } from "./route";

describe("POST /api/codec-analysis/convert: hardlink check", () => {
  let dir: string;

  beforeEach(async () => {
    dir = await mkdtemp(path.join(tmpdir(), "wheelhouse-convert-"));
    queue.enqueueConversion.mockClear();
    queue.findActiveJobForPath.mockReset();
    queue.findActiveJobForPath.mockReturnValue(undefined);
  });
  afterEach(() => rm(dir, { recursive: true, force: true }));

  async function file(name: string) {
    const p = path.join(dir, name);
    await writeFile(p, "not really a video");
    return p;
  }

  function post(filePath: string, extra: Record<string, unknown> = {}) {
    return POST(
      new Request("http://localhost/api/codec-analysis/convert", {
        method: "POST",
        body: JSON.stringify({
          instanceId: "radarr",
          itemId: 1,
          filePath,
          title: "Example Movie",
          originalSize: 18,
          backend: "software",
          targetFamily: "hevc",
          ...extra,
        }),
      })
    );
  }

  it("starts a conversion for a file with a single link", async () => {
    const res = await post(await file("movie.mkv"));
    expect(res.status).toBe(200);
    expect(await res.json()).toMatchObject({ id: "job-1" });
    expect(queue.enqueueConversion).toHaveBeenCalledOnce();
  });

  it("answers 409 with code 'hardlinked' for a file that has another hard link", async () => {
    const library = await file("movie.mkv");
    await link(library, path.join(dir, "seeding-copy.mkv"));

    const res = await post(library);
    expect(res.status).toBe(409);
    const body = await res.json();
    expect(body).toMatchObject({ code: "hardlinked", linkCount: 2 });
    expect(body.error).toContain("2 hard links");
    expect(queue.enqueueConversion).not.toHaveBeenCalled();
  });

  it("counts every extra link", async () => {
    const library = await file("movie.mkv");
    await link(library, path.join(dir, "a.mkv"));
    await link(library, path.join(dir, "b.mkv"));
    expect(await linkCount(library)).toBe(3);
    expect(await (await post(library)).json()).toMatchObject({ linkCount: 3 });
  });

  it("proceeds once the request carries the acknowledgement", async () => {
    const library = await file("movie.mkv");
    await link(library, path.join(dir, "seeding-copy.mkv"));

    const res = await post(library, { acknowledgeHardlink: true });
    expect(res.status).toBe(200);
    expect(queue.enqueueConversion).toHaveBeenCalledOnce();
    // The flag is for the route only; the queue never sees it.
    expect(queue.enqueueConversion.mock.calls[0][0]).not.toHaveProperty("acknowledgeHardlink");
  });

  it("only a real true acknowledges", async () => {
    const library = await file("movie.mkv");
    await link(library, path.join(dir, "seeding-copy.mkv"));
    for (const value of ["true", 1, "yes", null]) {
      expect((await post(library, { acknowledgeHardlink: value })).status).toBe(409);
    }
    expect(queue.enqueueConversion).not.toHaveBeenCalled();
  });

  it("stops asking once the other link is gone", async () => {
    const library = await file("movie.mkv");
    const copy = path.join(dir, "seeding-copy.mkv");
    await link(library, copy);
    expect((await post(library)).status).toBe(409);
    await unlink(copy);
    expect((await post(library)).status).toBe(200);
  });

  it("a separate copy of the same bytes is not a hard link", async () => {
    const library = await file("movie.mkv");
    await writeFile(path.join(dir, "copy.mkv"), "not really a video");
    expect((await post(library)).status).toBe(200);
  });

  it("leaves a missing file to the queue, which reports it on the job", async () => {
    const res = await post(path.join(dir, "gone.mkv"));
    expect(res.status).toBe(200);
    expect(queue.enqueueConversion).toHaveBeenCalledOnce();
  });

  it("doesn't ask again for a file that already has a job", async () => {
    const library = await file("movie.mkv");
    await link(library, path.join(dir, "seeding-copy.mkv"));
    queue.findActiveJobForPath.mockReturnValue({ id: "running" });
    expect((await post(library)).status).toBe(200);
  });

  it("still rejects a malformed body with 400", async () => {
    const res = await POST(
      new Request("http://localhost/api/codec-analysis/convert", {
        method: "POST",
        body: JSON.stringify({ filePath: "/x" }),
      })
    );
    expect(res.status).toBe(400);
  });
});

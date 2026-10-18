import { spawn, type ChildProcess } from "node:child_process";
import { access, readdir, rename, rm, stat, unlink } from "node:fs/promises";
import path from "node:path";
import { randomUUID } from "node:crypto";
import { getRuntime, type MediaManager } from "./providers";
import { ffmpegArgsFor, type Backend } from "./hw-encode";
import type { PreferableFamily } from "./settings";

export type ConversionStatus = "queued" | "probing" | "running" | "success" | "failed" | "canceled";

const ACTIVE_STATUSES: ConversionStatus[] = ["queued", "probing", "running"];

export interface ConversionJob {
  id: string;
  instanceId: string;
  itemId: number;
  filePath: string;
  title: string;
  originalSize: number;
  backend: Backend;
  targetFamily: PreferableFamily;
  status: ConversionStatus;
  progressPercent: number;
  error?: string;
  newSize?: number;
  backupPath?: string;
  log: string;
  createdAt: number;
  startedAt?: number;
  finishedAt?: number;
}

interface InternalJob extends ConversionJob {
  child?: ChildProcess;
  canceled?: boolean;
}

class CanceledError extends Error {}

const MAX_LOG_CHARS = 8000;
const TEMP_PREFIX = ".wheelhouse-convert-";
const MEDIA_ROOTS = ["/movies", "/tv"];

// Module-level singleton queue — there's no database/job infra in this app
// (see docs/architecture.md), and jobs only need to survive within one Node
// process's lifetime. Guarded via globalThis so a Next.js dev-mode module
// reload doesn't spawn a second, disconnected queue (or re-run startup
// recovery twice).
const g = globalThis as unknown as {
  __wheelhouseConvertJobs?: InternalJob[];
  __wheelhouseConvertRecovered?: boolean;
};
const jobs: InternalJob[] = g.__wheelhouseConvertJobs ?? (g.__wheelhouseConvertJobs = []);

let processing = false;

function stripInternal(job: InternalJob): ConversionJob {
  const { child, canceled, ...rest } = job;
  void child;
  void canceled;
  return rest;
}

function appendLog(job: InternalJob, text: string) {
  job.log = (job.log + text).slice(-MAX_LOG_CHARS);
}

export function listJobs(): ConversionJob[] {
  return jobs.map(stripInternal);
}

export function getJob(id: string): ConversionJob | undefined {
  const job = jobs.find((j) => j.id === id);
  return job && stripInternal(job);
}

export function findActiveJobForPath(filePath: string): ConversionJob | undefined {
  const job = jobs.find((j) => j.filePath === filePath && ACTIVE_STATUSES.includes(j.status));
  return job && stripInternal(job);
}

// A file that already has a queued/running job returns that job instead of
// starting a second one — two ffmpeg jobs against the same path would race
// on the same temp/backup filenames, and the second job's rename step would
// clobber the first job's backup with the first job's (already converted)
// output, losing the true original.
export function enqueueConversion(input: {
  instanceId: string;
  itemId: number;
  filePath: string;
  title: string;
  originalSize: number;
  backend: Backend;
  targetFamily: PreferableFamily;
}): ConversionJob {
  const existing = findActiveJobForPath(input.filePath);
  if (existing) return existing;

  const job: InternalJob = {
    id: randomUUID(),
    ...input,
    status: "queued",
    progressPercent: 0,
    log: "",
    createdAt: Date.now(),
  };
  jobs.push(job);
  void processQueue();
  return stripInternal(job);
}

// Only queued/running jobs can be canceled — queued ones are just marked
// canceled outright (nothing to kill yet), running ones get their ffmpeg
// process killed and runJob's catch block does the actual cleanup.
export function cancelJob(id: string): boolean {
  const job = jobs.find((j) => j.id === id);
  if (!job) return false;
  if (job.status === "queued") {
    job.status = "canceled";
    job.finishedAt = Date.now();
    return true;
  }
  if (job.status === "running" || job.status === "probing") {
    job.canceled = true;
    job.child?.kill("SIGKILL");
    return true;
  }
  return false;
}

// One conversion at a time — transcoding is CPU-heavy and the host usually also
// runs Sonarr/Radarr/a media server live, so a global serial queue avoids
// hammering the box if multiple conversions get started.
async function processQueue() {
  if (processing) return;
  processing = true;
  try {
    for (;;) {
      const next = jobs.find((j) => j.status === "queued");
      if (!next) break;
      await runJob(next);
    }
  } finally {
    processing = false;
  }
}

function ffprobeDurationSeconds(filePath: string): Promise<number> {
  return new Promise((resolve, reject) => {
    const child = spawn("ffprobe", [
      "-v",
      "error",
      "-show_entries",
      "format=duration",
      "-of",
      "default=noprint_wrappers=1:nokey=1",
      filePath,
    ]);
    let out = "";
    child.stdout.on("data", (d) => (out += d));
    child.on("error", reject);
    child.on("close", (code) => {
      const seconds = Number(out.trim());
      if (code === 0 && Number.isFinite(seconds) && seconds > 0) resolve(seconds);
      else reject(new Error("ffprobe couldn't read the file's duration"));
    });
  });
}

function runFfmpeg(job: InternalJob, tempPath: string, durationSeconds: number): Promise<void> {
  return new Promise((resolve, reject) => {
    const args = ffmpegArgsFor(job.backend, job.targetFamily, job.filePath, tempPath);
    appendLog(job, `$ ffmpeg ${args.join(" ")}\n`);
    const child = spawn("ffmpeg", args);
    job.child = child;

    // ffmpeg's -progress writes key=value lines to stdout; out_time_ms is
    // the running encode position, used against the probed total duration
    // to compute a percentage. stderr carries ffmpeg's normal human-readable
    // banner/stream-mapping/encoding log — surfaced verbatim as "verbose
    // output" in the modal.
    child.stderr.on("data", (d) => appendLog(job, d.toString()));

    let buf = "";
    child.stdout.on("data", (d) => {
      buf += d.toString();
      const lines = buf.split("\n");
      buf = lines.pop() ?? "";
      for (const line of lines) {
        const [key, value] = line.split("=");
        if (key === "out_time_ms") {
          const ms = Number(value);
          if (Number.isFinite(ms)) {
            const pct = (ms / 1_000_000 / durationSeconds) * 100;
            job.progressPercent = Math.max(0, Math.min(99, Math.round(pct)));
          }
        }
      }
    });

    child.on("error", reject);
    child.on("close", (code) => {
      job.child = undefined;
      if (job.canceled) return reject(new CanceledError());
      if (code === 0) resolve();
      else reject(new Error(`ffmpeg exited with code ${code}`));
    });
  });
}

async function runJob(job: InternalJob) {
  job.status = "probing";
  job.startedAt = Date.now();
  appendLog(job, `Probing ${job.filePath}\n`);
  appendLog(job, `Target: ${job.targetFamily} via ${job.backend}\n`);

  const dir = path.dirname(job.filePath);
  const ext = path.extname(job.filePath);
  // Leading dot hides the in-progress file from Radarr/Sonarr's own library
  // scanners, which ignore dotfiles — so a mid-conversion crawl won't pick
  // up a half-written file as a second copy of the movie/episode.
  const tempPath = path.join(dir, `${TEMP_PREFIX}${job.id}${ext}`);
  // Same-folder, non-video-extension backup rather than deleting the
  // original outright — Radarr/Sonarr's scanners match on known video
  // extensions, so a trailing `.bak` keeps this from being picked up as an
  // alternate file version. Nothing deletes this automatically.
  const backupPath = `${job.filePath}.bak`;

  try {
    await access(job.filePath);
    if (job.canceled) throw new CanceledError();

    const durationSeconds = await ffprobeDurationSeconds(job.filePath);
    appendLog(job, `Duration: ${durationSeconds.toFixed(1)}s\n`);
    if (job.canceled) throw new CanceledError();

    job.status = "running";
    await runFfmpeg(job, tempPath, durationSeconds);

    const outStat = await stat(tempPath);
    if (outStat.size <= 0) throw new Error("Converted file came out empty");

    await rename(job.filePath, backupPath);
    await rename(tempPath, job.filePath);
    appendLog(job, `Swapped in converted file; original kept at ${backupPath}\n`);

    await getRuntime()
      .getClient<MediaManager>(job.instanceId)
      .then((client) => client.rescan(job.itemId))
      .catch(() => {});

    job.status = "success";
    job.progressPercent = 100;
    job.newSize = outStat.size;
    job.backupPath = backupPath;
    job.finishedAt = Date.now();
  } catch (err) {
    // Update status *before* the cleanup unlink, not after — deleting a
    // large partially-written temp file on the NFS mount can take real
    // time, and the job must stop reporting "running" the moment it's
    // actually canceled, not once cleanup happens to finish. A poller
    // hitting GET mid-unlink used to still see "running" for however long
    // the delete took.
    if (err instanceof CanceledError) {
      job.status = "canceled";
      appendLog(job, "Canceled.\n");
    } else {
      job.status = "failed";
      job.error = err instanceof Error ? err.message : "conversion failed";
      appendLog(job, `Failed: ${job.error}\n`);
    }
    job.finishedAt = Date.now();
    await unlink(tempPath).catch(() => {});
  }
}

// Startup recovery: job state is in-memory only, so a container
// restart/redeploy mid-conversion loses all track of what was running. Two
// artifacts can be left behind by a killed process, both walked for and
// cleaned up once per process lifetime:
//
// 1. `.wheelhouse-convert-*` temp files — an interrupted encode's partial
//    output. Always safe to delete outright; nothing else references them
//    ("ditch and start over" rather than trying to resume an encode).
// 2. `<file>.bak` with no `<file>` next to it — means the process was
//    killed between the two renames in runJob() (original → .bak, then
//    temp → original), so the real content is stranded at the backup path
//    and the expected path is empty. Self-healed by renaming it back.
//    A `.bak` *with* a live `<file>` next to it is a normal, intentional
//    post-success backup and is left alone.
async function cleanupOrphanedArtifacts() {
  for (const root of MEDIA_ROOTS) {
    await walkAndClean(root).catch(() => {});
  }
}

async function walkAndClean(dir: string) {
  let entries;
  try {
    entries = await readdir(dir, { withFileTypes: true });
  } catch {
    return;
  }
  for (const entry of entries) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      await walkAndClean(full);
      continue;
    }
    if (entry.name.startsWith(TEMP_PREFIX)) {
      await rm(full, { force: true })
        .then(() => console.log(`[convert-queue] removed orphaned temp file ${full}`))
        .catch(() => {});
      continue;
    }
    if (entry.name.endsWith(".bak")) {
      const originalPath = full.slice(0, -".bak".length);
      const originalExists = await access(originalPath)
        .then(() => true)
        .catch(() => false);
      if (!originalExists) {
        await rename(full, originalPath)
          .then(() =>
            console.log(
              `[convert-queue] restored interrupted conversion: ${full} -> ${originalPath}`
            )
          )
          .catch((err) => console.error(`[convert-queue] failed to restore ${full}:`, err));
      }
    }
  }
}

if (!g.__wheelhouseConvertRecovered) {
  g.__wheelhouseConvertRecovered = true;
  void cleanupOrphanedArtifacts();
}

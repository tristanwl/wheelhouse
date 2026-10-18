import { spawn } from "node:child_process";
import { access } from "node:fs/promises";
import type { PreferableFamily } from "./settings";

export type Backend = "software" | "vaapi" | "nvenc";

export interface EncoderOption {
  backend: Backend;
  family: PreferableFamily;
  label: string;
  available: boolean;
  reason?: string;
}

// The only device this was actually built/tested against is an Intel
// iGPU (Haswell, Quick Sync). VAAPI also covers AMD GPUs on other installs; NVENC is probed
// separately below since it doesn't go through /dev/dri at all. Neither
// probe assumes a specific vendor — both just ask ffmpeg "does this
// encoder actually work," so this generalizes to whatever GPU a given
// install actually has (the whole point of making this detected rather
// than hardcoded, ahead of open-sourcing).
const VAAPI_DEVICE = "/dev/dri/renderD128";

function run(cmd: string, args: string[]): Promise<{ code: number | null; stderr: string }> {
  return new Promise((resolve) => {
    const child = spawn(cmd, args);
    let stderr = "";
    child.stderr.on("data", (d) => (stderr += d));
    child.on("error", () => resolve({ code: -1, stderr: `failed to spawn ${cmd}` }));
    child.on("close", (code) => resolve({ code, stderr }));
  });
}

function vaapiEncoderName(family: PreferableFamily): string {
  return family === "h264" ? "h264_vaapi" : "hevc_vaapi";
}

function nvencEncoderName(family: PreferableFamily): string {
  return family === "h264" ? "h264_nvenc" : "hevc_nvenc";
}

// A device node existing, or an encoder name showing up in `ffmpeg
// -encoders`, can both be true while the actual path is broken (missing/
// mismatched driver package, host permissions not passed through to the
// container, no such GPU generation support, etc.) — so every option here
// is confirmed with a real 1-second throwaway encode, not inferred.
async function probeVaapi(family: PreferableFamily): Promise<EncoderOption> {
  const label = `VAAPI (${family === "h264" ? "H.264" : "HEVC"})`;
  const deviceExists = await access(VAAPI_DEVICE)
    .then(() => true)
    .catch(() => false);
  if (!deviceExists) {
    return {
      backend: "vaapi",
      family,
      label,
      available: false,
      reason: `${VAAPI_DEVICE} not present in this container`,
    };
  }

  const { code, stderr } = await run("ffmpeg", [
    "-y",
    "-hide_banner",
    "-vaapi_device",
    VAAPI_DEVICE,
    "-f",
    "lavfi",
    "-i",
    "testsrc=duration=1:size=320x240:rate=5",
    "-vf",
    "format=nv12,hwupload",
    "-c:v",
    vaapiEncoderName(family),
    "-f",
    "null",
    "-",
  ]);

  if (code === 0) return { backend: "vaapi", family, label, available: true };
  return {
    backend: "vaapi",
    family,
    label,
    available: false,
    reason: stderr.trim().slice(-300) || `ffmpeg exited ${code}`,
  };
}

// NVENC doesn't go through /dev/dri or need an explicit device path — it
// auto-selects the first CUDA-capable NVIDIA GPU ffmpeg's nvenc build can
// see. Untested on real hardware (the dev machine's only NVIDIA GPU runs
// the open-source nouveau driver, which has no working NVENC), included so
// installs with a proper NVIDIA driver get this detected rather than
// needing code changes.
async function probeNvenc(family: PreferableFamily): Promise<EncoderOption> {
  const label = `NVIDIA NVENC (${family === "h264" ? "H.264" : "HEVC"})`;
  const { code, stderr } = await run("ffmpeg", [
    "-y",
    "-hide_banner",
    "-f",
    "lavfi",
    "-i",
    "testsrc=duration=1:size=320x240:rate=5",
    "-c:v",
    nvencEncoderName(family),
    "-f",
    "null",
    "-",
  ]);

  if (code === 0) return { backend: "nvenc", family, label, available: true };
  return {
    backend: "nvenc",
    family,
    label,
    available: false,
    reason: stderr.trim().slice(-300) || `ffmpeg exited ${code}`,
  };
}

function softwareOption(family: PreferableFamily): EncoderOption {
  return {
    backend: "software",
    family,
    label: family === "h264" ? "Software (x264)" : "Software (x265)",
    available: true,
  };
}

// GPU capability doesn't change while the container is running — cache the
// (real, spawned) probe results behind a globalThis singleton, same
// pattern as the job queue, so probing only ever runs once per process
// lifetime.
const g = globalThis as unknown as { __wheelhouseEncoderProbe?: Promise<EncoderOption[]> };

export function detectEncoders(): Promise<EncoderOption[]> {
  if (!g.__wheelhouseEncoderProbe) {
    g.__wheelhouseEncoderProbe = Promise.all([
      probeVaapi("h264"),
      probeVaapi("hevc"),
      probeNvenc("h264"),
      probeNvenc("hevc"),
    ]).then((hardware) => [softwareOption("h264"), softwareOption("hevc"), ...hardware]);
  }
  return g.__wheelhouseEncoderProbe;
}

// -qp / -cq are each backend's constant-quality knob — none of these are a
// precise equivalent of libx264/libx265's CRF scale, but each is the
// standard practical default for a comparable-quality encode on that
// backend.
export function ffmpegArgsFor(
  backend: Backend,
  family: PreferableFamily,
  filePath: string,
  tempPath: string
): string[] {
  if (backend === "vaapi") {
    return [
      "-y",
      "-vaapi_device",
      VAAPI_DEVICE,
      "-i",
      filePath,
      "-map",
      "0",
      "-vf",
      "format=nv12,hwupload",
      "-c:v",
      vaapiEncoderName(family),
      "-qp",
      "20",
      "-c:a",
      "copy",
      "-c:s",
      "copy",
      "-progress",
      "pipe:1",
      "-nostats",
      tempPath,
    ];
  }

  if (backend === "nvenc") {
    return [
      "-y",
      "-i",
      filePath,
      "-map",
      "0",
      "-c:v",
      nvencEncoderName(family),
      "-cq",
      "20",
      "-preset",
      "p5",
      "-pix_fmt",
      "yuv420p",
      "-c:a",
      "copy",
      "-c:s",
      "copy",
      "-progress",
      "pipe:1",
      "-nostats",
      tempPath,
    ];
  }

  // -map 0 -c copy pulls every stream (audio/subs/attachments) through
  // untouched; -c:v overrides just the video stream to re-encode. -pix_fmt
  // yuv420p forces 8-bit output even from a 10-bit source, since the whole
  // point of this tool is maximizing playback compatibility.
  return [
    "-y",
    "-i",
    filePath,
    "-map",
    "0",
    "-c",
    "copy",
    "-c:v",
    family === "h264" ? "libx264" : "libx265",
    "-preset",
    "medium",
    "-crf",
    "20",
    "-pix_fmt",
    "yuv420p",
    "-progress",
    "pipe:1",
    "-nostats",
    tempPath,
  ];
}

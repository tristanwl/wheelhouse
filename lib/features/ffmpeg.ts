import { spawn } from "node:child_process";

const g = globalThis as unknown as { __wheelhouseFfmpeg?: Promise<boolean> };

function probe(): Promise<boolean> {
  return new Promise((resolve) => {
    try {
      const child = spawn("ffmpeg", ["-version"], { stdio: "ignore", timeout: 5000 });
      child.on("error", () => resolve(false));
      child.on("close", (code) => resolve(code === 0));
    } catch {
      resolve(false);
    }
  });
}

/** Whether `ffmpeg -version` works. Checked once per server process, then cached. */
export function hasFfmpeg(): Promise<boolean> {
  return (g.__wheelhouseFfmpeg ??= probe());
}

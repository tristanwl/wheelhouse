import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";

export type PreferableFamily = "h264" | "hevc";

export interface CodecPreferences {
  hd: PreferableFamily;
  uhd: PreferableFamily;
}

// HD defaults to H.264 for maximum playback compatibility; UHD defaults to
// HEVC since its compression advantage over H.264 is largest at 4K (same
// reasoning the old hardcoded "leave 4K HEVC alone" rule used) — but this
// is now a preference, not a hardcoded rule, since a different install may
// have hardware (or none) that changes the right default for them.
export const DEFAULT_CODEC_PREFERENCES: CodecPreferences = {
  hd: "h264",
  uhd: "hevc",
};

// /config is a bind mount to a host directory (see
// deploy/wheelhouse.service.example and docker-compose.example.yml) so
// settings survive container rebuilds.
const CONFIG_DIR = "/config";
const SETTINGS_PATH = path.join(CONFIG_DIR, "settings.json");

interface SettingsFile {
  codecPreferences?: Partial<CodecPreferences>;
}

function isPreferableFamily(v: unknown): v is PreferableFamily {
  return v === "h264" || v === "hevc";
}

export async function getCodecPreferences(): Promise<CodecPreferences> {
  try {
    const raw = await readFile(SETTINGS_PATH, "utf-8");
    const parsed = JSON.parse(raw) as SettingsFile;
    const prefs = parsed.codecPreferences;
    return {
      hd: isPreferableFamily(prefs?.hd) ? prefs.hd : DEFAULT_CODEC_PREFERENCES.hd,
      uhd: isPreferableFamily(prefs?.uhd) ? prefs.uhd : DEFAULT_CODEC_PREFERENCES.uhd,
    };
  } catch {
    return DEFAULT_CODEC_PREFERENCES;
  }
}

export async function setCodecPreferences(prefs: CodecPreferences): Promise<void> {
  await mkdir(CONFIG_DIR, { recursive: true }).catch(() => {});
  const file: SettingsFile = { codecPreferences: prefs };
  await writeFile(SETTINGS_PATH, JSON.stringify(file, null, 2));
}

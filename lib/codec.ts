import {
  DEFAULT_CODEC_PREFERENCES,
  type CodecPreferences,
  type PreferableFamily,
} from "./settings";

export type CodecFamily = "h264" | "hevc" | "legacy";
export type Tier = "hd" | "uhd";

const H264_NAMES = new Set(["x264", "h264", "avc"]);
const HEVC_NAMES = new Set(["x265", "h265", "hevc"]);

// Radarr/Sonarr's mediaInfo reports the *encoder* name, not a clean codec
// family — "x264"/"h264"/"avc" all mean plain H.264, "x265"/"h265"/"hevc"
// all mean HEVC. Anything else (vc1, xvid, mpeg2, mpeg4, ...) is legacy.
export function classifyCodec(videoCodec: string): CodecFamily {
  const normalized = videoCodec.trim().toLowerCase();
  if (H264_NAMES.has(normalized)) return "h264";
  if (HEVC_NAMES.has(normalized)) return "hevc";
  return "legacy";
}

// Same threshold used both to classify HD vs UHD and, previously, to gate
// the old hardcoded "leave 4K HEVC alone" rule — HEVC's compression
// advantage over H.264 is largest at 4K, which is exactly why UHD's
// *default* preference (not a hardcoded rule anymore) is HEVC.
export const FOUR_K_THRESHOLD_PX = 3000;

function longEdgePx(resolution: string): number {
  const match = /^(\d+)x(\d+)$/.exec(resolution.trim());
  if (!match) return 0;
  return Math.max(Number(match[1]), Number(match[2]));
}

export function classifyTier(resolution: string | null): Tier {
  return longEdgePx(resolution ?? "") >= FOUR_K_THRESHOLD_PX ? "uhd" : "hd";
}

export interface CodecFileInfo {
  title: string;
  codec: string;
  resolution: string | null;
  bitDepth: number | null;
  size: number;
  // Identify + locate the underlying file for conversion: instanceId and
  // itemId (the movie or series id in that instance) are used to trigger a
  // rescan afterward; filePath is the absolute path as Radarr/Sonarr see it,
  // which only works if Wheelhouse mounts the media at the same paths
  // the *arr apps use.
  instanceId: string;
  itemId: number;
  filePath: string;
}

export interface CodecBucketSummary {
  count: number;
  totalSize: number;
  percentOfLibrary: number;
}

export type ConversionReason = "legacy-codec" | "codec-mismatch";

export interface ConversionCandidate extends CodecFileInfo {
  tier: Tier;
  // The codec this file's tier prefers — what "Convert" targets.
  targetFamily: PreferableFamily;
  reason: ConversionReason;
}

export interface MatchingItem extends CodecFileInfo {
  tier: Tier;
}

export interface CodecAnalysis {
  totalCount: number;
  totalSize: number;
  preferences: CodecPreferences;
  buckets: Record<CodecFamily, CodecBucketSummary>;
  candidates: ConversionCandidate[];
  // Files whose codec already matches their tier's preferred family —
  // renamed from the old hardcoded "leaveAsIs" (4K HEVC specifically) now
  // that "what's preferred" is configurable rather than fixed.
  matching: MatchingItem[];
}

// Not a "convert everything non-preferred" tool in spirit, even though the
// mechanism generalized: legacy codecs (essentially no hardware decode
// support anywhere) are always flagged toward whatever their tier prefers;
// H.264/HEVC files are flagged only when they don't already match their
// tier's preferred family. With the default preferences (hd: h264, uhd:
// hevc) this reproduces the old fixed behavior exactly — legacy always
// flagged, HEVC flagged only below 4K, and a 4K HEVC file left alone. A UHD
// file already in H.264 is *newly* a candidate under those defaults (convert
// up to HEVC for the space savings) — intentional, since the tool no longer
// hardcodes "H.264 is always fine everywhere."
export function analyzeCodecs(
  files: CodecFileInfo[],
  preferences: CodecPreferences = DEFAULT_CODEC_PREFERENCES
): CodecAnalysis {
  const totalCount = files.length;
  const totalSize = files.reduce((sum, f) => sum + f.size, 0);

  const byFamily: Record<CodecFamily, CodecFileInfo[]> = {
    h264: [],
    hevc: [],
    legacy: [],
  };
  for (const f of files) byFamily[classifyCodec(f.codec)].push(f);

  function bucket(family: CodecFamily): CodecBucketSummary {
    const list = byFamily[family];
    const size = list.reduce((sum, f) => sum + f.size, 0);
    return {
      count: list.length,
      totalSize: size,
      percentOfLibrary: totalSize > 0 ? (size / totalSize) * 100 : 0,
    };
  }

  const candidates: ConversionCandidate[] = [];
  const matching: MatchingItem[] = [];

  for (const f of files) {
    const family = classifyCodec(f.codec);
    const tier = classifyTier(f.resolution);
    const targetFamily = preferences[tier];

    if (family === "legacy") {
      candidates.push({ ...f, tier, targetFamily, reason: "legacy-codec" });
    } else if (family !== targetFamily) {
      candidates.push({ ...f, tier, targetFamily, reason: "codec-mismatch" });
    } else {
      matching.push({ ...f, tier });
    }
  }

  candidates.sort((a, b) => b.size - a.size);
  matching.sort((a, b) => b.size - a.size);

  return {
    totalCount,
    totalSize,
    preferences,
    buckets: { h264: bucket("h264"), hevc: bucket("hevc"), legacy: bucket("legacy") },
    candidates,
    matching,
  };
}

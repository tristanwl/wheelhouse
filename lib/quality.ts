export interface QualityProfile {
  id: number;
  name: string;
}

// The server's own quality-profile list ordering is arbitrary (often
// defaults to something broad like "Any" first) — prefer a profile that
// plainly means "HD" without extra qualifiers as the default shown when
// asking what quality to add at.
export function pickDefaultQualityProfileId(
  profiles: QualityProfile[]
): number | null {
  if (profiles.length === 0) return null;
  const exact = profiles.find(
    (p) => p.name.trim().toLowerCase() === "hd - 720p/1080p"
  );
  if (exact) return exact.id;
  const hdLike = profiles.find(
    (p) => /\bhd\b/i.test(p.name) && !/ultra/i.test(p.name)
  );
  if (hdLike) return hdLike.id;
  return profiles[0].id;
}

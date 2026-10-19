import type { HdrClass } from "./types";

/** The name shown for an HDR class; "unknown" is anything that looks like HDR but could not be classified. */
export function hdrLabel(hdr: HdrClass): string {
  switch (hdr) {
    case "hdr10":
      return "HDR10";
    case "hlg":
      return "HLG";
    case "dolby-vision":
      return "Dolby Vision";
    case "unknown":
      return "HDR?";
    default:
      return "SDR";
  }
}

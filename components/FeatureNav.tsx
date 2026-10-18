"use client";

import Link from "next/link";
import { useFeatures } from "@/lib/features/use-features";
import type { FeaturesResponse } from "@/lib/features/server";

/** Dashboard is always there; the rest show once the server says they're available. */
export function visibleFeatures(features: FeaturesResponse["features"] | undefined) {
  return (features ?? []).filter((f) => f.id === "dashboard" || f.available);
}

export function NavLinks({ features }: { features: FeaturesResponse["features"] | undefined }) {
  const links = visibleFeatures(features);
  return (
    <nav className="flex gap-4 text-sm text-slate-300">
      {(links.length > 0 ? links : [{ id: "dashboard", label: "Dashboard", route: "/" }]).map(
        (f) => (
          <Link key={f.id} href={f.route} className="hover:text-white">
            {f.label}
          </Link>
        )
      )}
    </nav>
  );
}

export function FeatureNav() {
  return <NavLinks features={useFeatures().features} />;
}

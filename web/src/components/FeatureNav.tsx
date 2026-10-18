import { Link } from "react-router";
import { useFeatures, visibleFeatures } from "@/lib/features";
import type { FeaturesResponse } from "@/lib/types";

export function NavLinks({ features }: { features: FeaturesResponse["features"] | undefined }) {
  const links = visibleFeatures(features);
  return (
    <nav className="flex gap-4 text-sm text-slate-300">
      {(links.length > 0 ? links : [{ id: "dashboard", label: "Dashboard", route: "/" }]).map(
        (f) => (
          <Link key={f.id} to={f.route} className="hover:text-white">
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

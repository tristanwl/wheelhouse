import { Link } from "react-router";
import { useFeatures, visibleFeatures } from "@/lib/features";
import { useSession } from "@/lib/session-context";
import type { FeaturesResponse } from "@/lib/types";

export function NavLinks({
  features,
  admin = false,
}: {
  features: FeaturesResponse["features"] | undefined;
  /** Add the Admin link (administrators only). */
  admin?: boolean;
}) {
  const links = visibleFeatures(features);
  return (
    <nav aria-label="Main" className="flex flex-wrap gap-x-4 gap-y-1 text-sm text-slate-300">
      {(links.length > 0 ? links : [{ id: "dashboard", label: "Dashboard", route: "/" }]).map(
        (f) => (
          <Link key={f.id} to={f.route} className="hover:text-white">
            {f.label}
          </Link>
        )
      )}
      {admin && (
        <Link to="/admin" className="hover:text-white">
          Admin
        </Link>
      )}
    </nav>
  );
}

export function FeatureNav() {
  const { isAdmin } = useSession();
  return <NavLinks features={useFeatures().features} admin={isAdmin} />;
}

import { Link } from "react-router";
import { useFeatures } from "@/lib/features";
import { useSession } from "@/lib/session-context";
import type { FeatureId } from "@/lib/types";

/** Shown instead of a page whose feature isn't available, so a direct visit explains itself. */
export function FeatureStub({ label, missing }: { label: string; missing: string[] }) {
  const { isAdmin } = useSession();
  if (missing.includes("disabled by admin")) {
    return (
      <div className="rounded-lg border border-slate-800 bg-slate-900 p-4 text-sm text-slate-300">
        <h1 className="mb-2 text-lg font-medium">{label} is switched off</h1>
        <p>
          An administrator switched this feature off (disabled by admin). Go back to the{" "}
          <Link to="/" className="underline hover:text-white">
            Dashboard
          </Link>
          .
        </p>
      </div>
    );
  }
  return (
    <div className="rounded-lg border border-slate-800 bg-slate-900 p-4 text-sm text-slate-300">
      <h1 className="mb-2 text-lg font-medium">{label} isn&apos;t available yet</h1>
      <p>
        It needs {missing.length > 0 ? missing.join(" and ") : "more configuration"}.{" "}
        {isAdmin ? (
          <>
            Add it under{" "}
            <Link to="/admin/services" className="underline hover:text-white">
              Admin, Services
            </Link>
            .
          </>
        ) : (
          "Ask an administrator to set it up."
        )}{" "}
        If you have already set it up, the{" "}
        <Link to="/" className="underline hover:text-white">
          Dashboard
        </Link>{" "}
        shows what is wrong with each service.
      </p>
    </div>
  );
}

/**
 * Renders the page only when its feature is available. While the feature list
 * loads it renders nothing; if the list can't be loaded it lets the page
 * through, since the page reports its own errors.
 */
export function FeatureGate({ id, children }: { id: FeatureId; children: React.ReactNode }) {
  const { features, error } = useFeatures();
  if (error) return <>{children}</>;
  const feature = features?.find((f) => f.id === id);
  if (!feature) return null;
  return feature.available ? (
    <>{children}</>
  ) : (
    <FeatureStub label={feature.label} missing={feature.missing} />
  );
}

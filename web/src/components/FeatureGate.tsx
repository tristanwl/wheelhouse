import { Link } from "react-router";
import { README_CONFIG_URL, useFeatures } from "@/lib/features";
import type { FeatureId } from "@/lib/types";

/** Shown instead of a page whose feature isn't available, so a direct visit explains itself. */
export function FeatureStub({ label, missing }: { label: string; missing: string[] }) {
  return (
    <div className="rounded-lg border border-slate-800 bg-slate-900 p-4 text-sm text-slate-300">
      <h1 className="mb-2 text-lg font-medium">{label} isn&apos;t available yet</h1>
      <p>
        It needs {missing.length > 0 ? missing.join(" and ") : "more configuration"}. See the{" "}
        <a
          href={README_CONFIG_URL}
          target="_blank"
          rel="noopener noreferrer"
          className="underline hover:text-white"
        >
          Configuration section of the README
        </a>
        . If you have already set it up, the{" "}
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

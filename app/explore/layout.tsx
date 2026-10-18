import { FeatureGate } from "@/components/FeatureGate";

export default function ExploreLayout({ children }: { children: React.ReactNode }) {
  return <FeatureGate id="explore">{children}</FeatureGate>;
}

import { FeatureGate } from "@/components/FeatureGate";

export default function LibraryLayout({ children }: { children: React.ReactNode }) {
  return <FeatureGate id="library">{children}</FeatureGate>;
}

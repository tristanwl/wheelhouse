import { FeatureGate } from "@/components/FeatureGate";

export default function CodecsLayout({ children }: { children: React.ReactNode }) {
  return <FeatureGate id="codecs">{children}</FeatureGate>;
}

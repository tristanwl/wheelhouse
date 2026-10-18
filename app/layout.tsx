import type { Metadata } from "next";
import { Bricolage_Grotesque } from "next/font/google";
import "./globals.css";
import { AddSearchGate } from "@/components/AddSearchGate";
import { Logo } from "@/components/Logo";
import { FeatureNav } from "@/components/FeatureNav";

const logoFont = Bricolage_Grotesque({
  subsets: ["latin"],
  weight: "700",
  variable: "--font-logo",
  display: "swap",
});

export const metadata: Metadata = {
  title: "wheelhouse",
  description: "A dashboard and companion for your *arr media stack",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className={logoFont.variable}>
      <body>
        <div className="mx-auto max-w-5xl px-4 py-6">
          <header className="mb-6 flex items-center justify-between">
            <Logo />
            <FeatureNav />
          </header>
          <AddSearchGate />
          {children}
        </div>
      </body>
    </html>
  );
}

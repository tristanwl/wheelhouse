import type { Metadata } from "next";
import Link from "next/link";
import "./globals.css";
import { AddSearchGate } from "@/components/AddSearchGate";
import { FeatureNav } from "@/components/FeatureNav";

export const metadata: Metadata = {
  title: "Wheelhouse",
  description: "A dashboard and companion for your *arr media stack",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>
        <div className="mx-auto max-w-5xl px-4 py-6">
          <header className="mb-6 flex items-center justify-between">
            <Link href="/" className="text-xl font-semibold hover:text-slate-300">
              Wheelhouse
            </Link>
            <FeatureNav />
          </header>
          <AddSearchGate />
          {children}
        </div>
      </body>
    </html>
  );
}

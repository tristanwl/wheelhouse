import type { Metadata } from "next";
import Link from "next/link";
import "./globals.css";
import { AddSearch } from "@/components/AddSearch";

export const metadata: Metadata = {
  title: "Wheelhouse",
  description: "A dashboard and companion for your *arr media stack",
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body>
        <div className="mx-auto max-w-5xl px-4 py-6">
          <header className="mb-6 flex items-center justify-between">
            <Link href="/" className="text-xl font-semibold hover:text-slate-300">
              Wheelhouse
            </Link>
            <nav className="flex gap-4 text-sm text-slate-300">
              <Link href="/" className="hover:text-white">
                Dashboard
              </Link>
              <Link href="/library" className="hover:text-white">
                Library
              </Link>
              <Link href="/explore" className="hover:text-white">
                Explore
              </Link>
              <Link href="/codecs" className="hover:text-white">
                Codecs
              </Link>
            </nav>
          </header>
          <div className="mb-6">
            <AddSearch />
          </div>
          {children}
        </div>
      </body>
    </html>
  );
}

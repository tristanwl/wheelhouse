import { useEffect } from "react";
import { Outlet, useLocation } from "react-router";
import { AddSearchGate } from "./AddSearchGate";
import { FeatureNav } from "./FeatureNav";
import { Logo } from "./Logo";

/**
 * A new page starts at the top, as it did under Next. (React Router's own
 * ScrollRestoration renders an inline script, which the page's CSP forbids.)
 */
function ScrollToTop() {
  const { pathname } = useLocation();
  useEffect(() => {
    window.scrollTo?.(0, 0);
  }, [pathname]);
  return null;
}

/** The frame around every page: logo and nav, then the add/search box when Library is available. */
export function Layout() {
  return (
    <div className="mx-auto max-w-5xl px-4 py-6">
      <header className="mb-6 flex items-center justify-between">
        <Logo />
        <FeatureNav />
      </header>
      <AddSearchGate />
      <Outlet />
      <ScrollToTop />
    </div>
  );
}

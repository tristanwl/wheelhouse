import type { RouteObject } from "react-router";
import { FeatureGate } from "./components/FeatureGate";
import { Layout } from "./components/Layout";
import CodecsPage from "./pages/CodecsPage";
import DashboardPage from "./pages/DashboardPage";
import ExploreCategoryPage from "./pages/ExploreCategoryPage";
import ExplorePage from "./pages/ExplorePage";
import LibraryPage from "./pages/LibraryPage";
import NotFoundPage from "./pages/NotFoundPage";

/**
 * The same URLs the Next app had, so deep links keep working. Library, Explore
 * and Codecs sit behind their feature: a page the server says is not available
 * shows the "not configured" stub instead.
 */
export const routes: RouteObject[] = [
  {
    element: <Layout />,
    children: [
      { path: "/", element: <DashboardPage /> },
      {
        path: "/library",
        element: (
          <FeatureGate id="library">
            <LibraryPage />
          </FeatureGate>
        ),
      },
      {
        path: "/explore",
        element: (
          <FeatureGate id="explore">
            <ExplorePage />
          </FeatureGate>
        ),
      },
      {
        path: "/explore/:category",
        element: (
          <FeatureGate id="explore">
            <ExploreCategoryPage />
          </FeatureGate>
        ),
      },
      {
        path: "/codecs",
        element: (
          <FeatureGate id="codecs">
            <CodecsPage />
          </FeatureGate>
        ),
      },
      { path: "*", element: <NotFoundPage /> },
    ],
  },
];

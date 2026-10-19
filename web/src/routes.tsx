import type { RouteObject } from "react-router";
import { FeatureGate } from "./components/FeatureGate";
import { AuthFrame } from "./components/Form";
import { RequireAdmin, RequireAuth } from "./components/Guards";
import { Layout } from "./components/Layout";
import { SessionRoot } from "./components/SessionProvider";
import AccountPage from "./pages/AccountPage";
import ChangePasswordPage from "./pages/ChangePasswordPage";
import CodecsPage from "./pages/CodecsPage";
import DashboardPage from "./pages/DashboardPage";
import ExploreCategoryPage from "./pages/ExploreCategoryPage";
import ExplorePage from "./pages/ExplorePage";
import LibraryPage from "./pages/LibraryPage";
import LoginPage from "./pages/LoginPage";
import NotFoundPage from "./pages/NotFoundPage";
import SetupPage from "./pages/SetupPage";

/**
 * The same URLs the Next app had, so deep links keep working, plus the
 * authentication pages. Only /login and /setup are open to someone who is not
 * signed in; every other page sits behind `RequireAuth`. `/admin/*` is reserved
 * for the administration pages (plan item 7), which replace the placeholder
 * children below; `RequireAdmin` already keeps everyone but administrators out.
 * Library, Explore and Codecs sit behind their feature: a page the server says is
 * not available shows the "not configured" stub instead.
 */
export const routes: RouteObject[] = [
  {
    element: <SessionRoot />,
    children: [
      {
        element: <AuthFrame />,
        children: [
          { path: "/login", element: <LoginPage /> },
          { path: "/setup", element: <SetupPage /> },
        ],
      },
      {
        element: <RequireAuth />,
        children: [
          {
            path: "/change-password",
            element: (
              <AuthFrame>
                <ChangePasswordPage />
              </AuthFrame>
            ),
          },
          {
            element: <Layout />,
            children: [
              { path: "/", element: <DashboardPage /> },
              { path: "/account", element: <AccountPage /> },
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
              {
                path: "/admin",
                element: <RequireAdmin />,
                children: [
                  { index: true, element: <NotFoundPage /> },
                  { path: "*", element: <NotFoundPage /> },
                ],
              },
              { path: "*", element: <NotFoundPage /> },
            ],
          },
        ],
      },
    ],
  },
];

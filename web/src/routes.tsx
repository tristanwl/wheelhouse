import { Navigate, type RouteObject } from "react-router";
import { AdminLayout } from "./components/admin/AdminLayout";
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
import AuditPage from "./pages/admin/AuditPage";
import ConverterPage from "./pages/admin/ConverterPage";
import FeaturesPage from "./pages/admin/FeaturesPage";
import NetworkPage from "./pages/admin/NetworkPage";
import ServiceFormPage from "./pages/admin/ServiceFormPage";
import ServicesPage from "./pages/admin/ServicesPage";
import SessionsPage from "./pages/admin/SessionsPage";
import UsersPage from "./pages/admin/UsersPage";

/**
 * The same URLs the Next app had, so deep links keep working, plus the
 * authentication pages. Only /login and /setup are open to someone who is not
 * signed in; every other page sits behind `RequireAuth`. `/admin/*` holds the
 * administration pages (plan item 7); `RequireAdmin` keeps everyone but
 * administrators out of all of them.
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
                  {
                    element: <AdminLayout />,
                    children: [
                      { index: true, element: <Navigate to="/admin/services" replace /> },
                      { path: "services", element: <ServicesPage /> },
                      { path: "services/new", element: <ServiceFormPage /> },
                      { path: "services/:id", element: <ServiceFormPage /> },
                      { path: "features", element: <FeaturesPage /> },
                      { path: "users", element: <UsersPage /> },
                      { path: "sessions", element: <SessionsPage /> },
                      { path: "network", element: <NetworkPage /> },
                      { path: "converter", element: <ConverterPage /> },
                      { path: "audit", element: <AuditPage /> },
                      { path: "*", element: <NotFoundPage /> },
                    ],
                  },
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

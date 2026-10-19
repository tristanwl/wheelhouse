import { Navigate, useNavigate, useSearchParams } from "react-router";
import { PasswordChangeForm } from "@/components/PasswordChangeForm";
import { safeRedirect } from "@/lib/redirect";
import { useSession } from "@/lib/session-context";

/**
 * Shown before anything else to an account whose password an administrator set
 * or reset (`mustChangePassword`): the server refuses every other request until
 * it is changed. Once it is, the person goes where they were headed.
 */
export default function ChangePasswordPage() {
  const session = useSession();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const redirect = safeRedirect(params.get("redirect"));

  if (!session.mustChangePassword) return <Navigate to={redirect} replace />;

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-lg font-medium">Choose a new password</h1>
        <p className="mt-1 text-sm text-slate-400">
          Your password was set by an administrator. Choose one only you know before you continue.
        </p>
      </div>
      <PasswordChangeForm autoFocus />
      <button
        type="button"
        onClick={() => void session.logout().then(() => navigate("/login", { replace: true }))}
        className="text-sm text-slate-400 underline hover:text-white"
      >
        Sign out instead
      </button>
    </div>
  );
}

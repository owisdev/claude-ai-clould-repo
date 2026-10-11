// Firebase sign-in for admins. The session lives in this browser tab only
// (sessionStorage): closing the tab signs out.
import { initializeApp } from "@firebase/app";
import {
  browserSessionPersistence,
  initializeAuth,
  onIdTokenChanged,
  sendEmailVerification,
  sendPasswordResetEmail,
  signInWithEmailAndPassword,
  signOut,
  type Auth,
  type User,
} from "@firebase/auth";

export interface AdminConfig {
  firebase_api_key: string;
  firebase_auth_domain: string;
  firebase_project_id: string;
  require_2fa: boolean;
}

let auth: Auth | null = null;

export async function loadConfig(): Promise<AdminConfig> {
  const res = await fetch("/admin/config.json", { cache: "no-store" });
  if (!res.ok) throw new Error(`config: HTTP ${res.status}`);
  return res.json();
}

export function initAuth(cfg: AdminConfig): Auth {
  if (auth) return auth;
  const app = initializeApp({
    apiKey: cfg.firebase_api_key,
    authDomain: cfg.firebase_auth_domain,
    projectId: cfg.firebase_project_id,
  });
  // No popup / redirect resolver: email + password only, no third-party frames.
  auth = initializeAuth(app, { persistence: browserSessionPersistence });
  return auth;
}

export function watchUser(cb: (u: User | null) => void): () => void {
  if (!auth) throw new Error("auth not initialized");
  return onIdTokenChanged(auth, cb);
}

export async function signIn(email: string, password: string): Promise<void> {
  if (!auth) throw new Error("auth not initialized");
  await signInWithEmailAndPassword(auth, email.trim(), password);
}

export async function resetPassword(email: string): Promise<void> {
  if (!auth) throw new Error("auth not initialized");
  await sendPasswordResetEmail(auth, email.trim());
}

export async function verifyEmail(): Promise<void> {
  if (auth?.currentUser) await sendEmailVerification(auth.currentUser);
}

export async function logOut(): Promise<void> {
  if (auth) await signOut(auth);
}

/** The current user's ID token (refreshed by Firebase when needed). */
export async function idToken(force = false): Promise<string | null> {
  return auth?.currentUser ? auth.currentUser.getIdToken(force) : null;
}

/** Readable text for Firebase sign-in errors. */
export function authErrorText(err: unknown): string {
  const code = (err as { code?: string })?.code ?? "";
  switch (code) {
    case "auth/invalid-credential":
    case "auth/wrong-password":
    case "auth/user-not-found":
    case "auth/invalid-email":
      return "Email or password is wrong.";
    case "auth/too-many-requests":
      return "Too many attempts. Wait a few minutes and try again.";
    case "auth/network-request-failed":
      return "No connection to Firebase.";
    case "auth/multi-factor-auth-required":
      return "This account uses two-step sign-in, which this panel does not support yet.";
    default:
      return err instanceof Error ? err.message : String(err);
  }
}

// What the sign-in page and the settings page say when signing in through the identity provider goes wrong. The
// server sends one of these codes in the address, never text the provider supplied.
export const SSO_ERRORS = {
  state: "That sign-in did not start here, or it took too long. Try again.",
  forbidden: "Only a signed-in admin can do that.",
  unavailable: "Sign-in is not available right now. Try again in a moment.",
  disabled: "Signing in through the identity provider is not turned on.",
  provider: "Geass could not complete the sign-in with the identity provider. An admin can see why in the operator log.",
  provider_denied: "The identity provider did not sign you in.",
  no_email: "The identity provider did not say what your email address is.",
  email_unverified: "The identity provider says your email address is not verified.",
  required_claim: "Your account is not one this installation lets in.",
  no_role: "Your account has no role here. Ask an admin to give you one at the identity provider.",
  no_org: "Your account is not in an organization set up here. Ask a platform administrator to add it.",
  org_pending: "Your organization was made for you and is waiting for a platform administrator to approve it. Try again once it has been.",
};

export const ssoErrorMessage = (code) => SSO_ERRORS[code] || "The sign-in did not go through.";

const REMEMBER_KEY = "geass.login.sso";

// A person who signs in through the provider each time should not have to press the button: the choice is remembered in
// this browser. Storage can be missing or refuse writes (a private window), and the page works without it.
export function rememberedSSO() {
  try {
    return window.localStorage.getItem(REMEMBER_KEY) === "1";
  } catch {
    return false;
  }
}

export function rememberSSO(on) {
  try {
    if (on) window.localStorage.setItem(REMEMBER_KEY, "1");
    else window.localStorage.removeItem(REMEMBER_KEY);
  } catch {
    // Not remembering is fine.
  }
}

const AUTO_KEY = "geass.login.sso.auto";
const AUTO_SPACING_MS = 30000;

// shouldAutoRedirect says whether the page may send the browser to the provider by itself, and notes that it did. It
// will not do it twice within half a minute: when the browser cannot keep the session cookie (the dashboard opened over
// plain http at an address other than localhost, where the cookie is marked Secure), signing in would otherwise go round
// and round between the dashboard and the provider. The button still works.
export function shouldAutoRedirect() {
  try {
    const last = Number(window.sessionStorage.getItem(AUTO_KEY) || 0);
    if (Date.now() - last < AUTO_SPACING_MS) return false;
    window.sessionStorage.setItem(AUTO_KEY, String(Date.now()));
  } catch {
    // Without storage there is nothing to tell a second time from a first, so do not do it by itself at all.
    return false;
  }
  return true;
}

// ssoLoginURL is where the browser goes to sign in, and to come back to the page it was on.
export function ssoLoginURL() {
  const here = new URLSearchParams(window.location.search);
  here.delete("sso_error");
  const query = here.toString();
  const next = `${window.location.pathname}${query ? `?${query}` : ""}`;
  return next === "/" ? "/auth/oidc/login" : `/auth/oidc/login?next=${encodeURIComponent(next)}`;
}

---
title: "Sign in with an identity provider"
description: "Let an identity provider such as Better Auth, Keycloak or Okta decide who signs in to the dashboard and with which role: set it up, test it, turn it on."
---

By default the people who can sign in to the dashboard are the users in a Kubernetes Secret, each with a password. That is enough for a few people, and it has no invitations, no sign-in by email code or link, and no organizations or teams. If you want those, connect an identity provider (and, to keep each organization's projects apart, see [organizations and workspaces](/docs/guides/organizations-and-workspaces/)). It owns the people: who exists, how they prove who they are, who was invited, which organization and team they are in. Geass does not grow any of that. It verifies the provider's signed answer and turns one claim of it into one of its two roles, **admin** or **viewer**.

Any provider that speaks OpenID Connect works: [Better Auth](https://www.better-auth.com), Keycloak, Authentik, Zitadel, Auth0, Okta, Google Workspace and others. Nothing here is specific to one of them except the recipe for Better Auth near the end.

Programs are not affected. [API tokens](/docs/guides/users-and-tokens/), deploy hooks and `geass login` work the same in both modes: a token is made by a signed-in admin, and is tied to who made it.

## What changes and what does not

| | Passwords from the Secret (the default) | Through an identity provider |
| --- | --- | --- |
| Who can sign in | The users in `geass-dashboard-auth` | Whoever the provider lets in and the role claim maps to a role |
| Invitations, codes, organizations, teams | None | At the provider |
| Roles | `admin` or `viewer`, set in the Secret | `admin` or `viewer`, from a claim of the provider's token |
| Role changes | Apply to open sessions | Apply when the person next signs in (a sign-in lasts an hour by default), or within minutes if you [check people again](#check-people-again-while-they-are-signed-in) |
| Removing someone | Delete the entry | Remove them at the provider |
| Tokens and deploy hooks | Unchanged | Unchanged |
| A user with a password in the Secret | Signs in | Signs in only if an admin. A viewer's password is refused, and turning the provider on ends their open sessions |

The admins with a password are the way back in. While the provider decides, a user in the Secret who is an admin can still sign in with a password, so that a provider that is down, or set up wrong, does not lock everyone out.

## Set it up

The provider is set up under **Settings** → **Sign-in**, by a signed-in admin. An API token cannot do it, not even an admin token: what decides who can sign in must not be changeable by something that outlives a person's access.

1. At the provider, register Geass as a client. It will ask for a redirect URI. Use the address the page shows, which is the dashboard's own address with `/auth/oidc/callback`, for example `https://geass.example.com/auth/oidc/callback`. Once the dashboard's domain is set up and verified, that is the address it is published at, whatever host a request arrives with, so the provider's redirect list never has to trust a host an attacker chose. Before that (no domain yet, or a dashboard you reach on `localhost`) it is the address you opened the page at, and you must sign in at that address. Note the client ID and, for a confidential client, the client secret.
2. In Geass, fill in the page and choose **Save**:

   | Field | What to put |
   | --- | --- |
   | Name | What the button says: **Sign in with** this |
   | Issuer URL | The provider's issuer, exactly as its discovery document names itself. It has to be an `https` URL. Geass reads `<issuer>/.well-known/openid-configuration` |
   | Client ID, Client secret | From the provider. Leave the secret empty for a public client that uses PKCE alone |
   | Role claim | The claim of the ID token that holds the person's role at the provider, such as `org_role`. A dotted path reaches into nested claims (`realm_access.roles`). Its value may be a string, a list, or a comma-separated string, which is how Better Auth stores a member who has several roles |
   | Roles | One per line, the provider's role then `=admin` or `=viewer`. A person whose roles are none of these cannot sign in. When someone has several, the highest wins |
   | Only people with | Optional but important, one per line as `claim=value`. `organization_slug=acme` lets in only the members of that organization. The claim may be a single value or a list that contains it, and it is compared whole: text is never split at commas, so an organization called `x, Acme` is not `Acme`. Without any, everyone the provider signs in with a mapped role gets in, so add one whenever anyone can sign up at the provider |
   | Scopes | Asked for besides `openid`. Empty means `email` and `profile` |
   | Session length | Minutes before a person goes back to the provider (5 to 720, default 60) |

   Saving stores the settings and does not connect to the provider, and does not turn anything on. (Geass keeps what a test found in memory, so an operator restart means testing again.)

   The client secret goes in the Secret `platform-auth` and is never shown again. It is kept only for the provider and client it was saved for: if you change the issuer or the client ID you are asked for the secret again, so that one provider's secret can never be handed to another.
3. Choose **Test sign-in**. Geass sends you to the provider, you sign in as someone who would be an admin here, and you come back to a page that says what it found: your email and the role it maps to, or why you would be turned away. The test does not sign anyone in.
4. Choose **Use ... to sign in**. It needs all of these, so that a typo cannot lock everyone out:
   - the settings are complete;
   - a test sign-in of exactly the settings as saved went through in the last half hour, as someone who maps to **admin**;
   - a user in the Secret is an admin with a password.

From then on the sign-in page leads with **Sign in with ...** and keeps **Use an admin password instead** below it. A person who signs in this way is remembered by the browser, and goes straight to the provider next time.

To change the provider's settings, first choose **Switch back to passwords**: while people sign in through the provider, the settings cannot be edited. **Remove** forgets the provider and its secret. Neither changes anything at the provider.

## Roles

The role is the one the provider gave when the person signed in, mapped through your **Roles** list. It is not looked up again, which is why a sign-in lasts a limited time (an hour by default). When it ends the person goes back to the provider. If they still have a session there, that is silent and picks up their current role; if they were removed or signed out there, they have to sign in, and cannot.

`observer` is a token role only; a person is an admin or a viewer.

A sign-in is refused, with a short message on the sign-in page, when:

| Message | Why |
| --- | --- |
| Your account has no role here | None of the person's roles at the provider is in your **Roles** list |
| Your account is not one this installation lets in | A claim in **Only people with** is missing or has another value |
| The identity provider did not say what your email address is | The ID token has no `email`. Ask for the `email` scope, or add the claim at the provider |
| The identity provider says your email address is not verified | The token says `email_verified` is false. Geass believes a provider that says no, and accepts one that does not say |
| That sign-in did not start here, or it took too long | The browser that came back is not the one that left, or it took more than ten minutes |
| Geass could not complete the sign-in with the identity provider | The provider could not be reached, refused the code, or sent a token that did not check out. The operator log says which |

People are named by their email address in the dashboard and in what they make. Geass tells them apart by the issuer and the subject, which a provider does not reuse, so a person whose email changes is still the same person.

## When someone leaves

Removing a person at the provider ends their access in two ways, and you should use both.

- **Short sessions.** Their next sign-in, at most one session length away, goes through the provider, which refuses them.
- **Checking people again.** With [a check every few minutes](#check-people-again-while-they-are-signed-in), Geass asks the provider whether each signed-in person is still its own, and ends the sessions of one that is not, without waiting for the session to end.
- **Back-channel logout.** When someone signs out at the provider or is removed, the provider can tell Geass at once. Give it the **Back-channel logout URI** the page shows, `https://geass.example.com/auth/oidc/backchannel-logout`. Geass then ends every session that person has, in every browser, and, unless you turn that off (below), revokes the API tokens and deploy hooks they made. A provider cannot say whether a person signed out or was removed, so with it on both do this: **an API token or deploy hook that CI depends on should not be made from the account of a person who signs out**; make it, with an expiry, from an account that does not.

Without the second, or with the switch below off, a token someone made while they had access keeps working until it expires or you revoke it under **Settings** → **Tokens**. Many providers insist that the logout address is a public `https` address they can reach, so it works for a dashboard published on a domain, and not for one you reach only through an SSH tunnel or on `localhost`.

Geass accepts a logout token only when it is signed by the provider, was issued for this client, has not expired and is less than five minutes old, carries the logout event, an identifier and a subject, and has no nonce. A token that names only a session, with no subject, cannot be matched to a person, and is refused. One it has already acted on is answered and ignored, so a replay does not sign anyone out of a later session. A source that keeps sending bad ones is slowed down.

**Sign out** in the dashboard ends all of that person's sessions in Geass, in every browser. It does not sign them out of the provider, where they stay signed in; to end that, sign out there.

### Keep tokens when someone signs out

Under the back-channel logout address, **Also revoke the tokens and deploy hooks they made** is on by default. Turn it off when the tokens and
hooks your CI uses were made from the accounts of people who sign out at the provider, and you would rather revoke them by hand when someone
leaves. A logout then ends the person's sessions and leaves what they made. It is the setting `spec.auth.oidc.revokeTokensOnLogout`, it is
saved at once, and unlike the rest of the provider's settings it can be changed while people sign in through the provider, because it does not
change who can sign in. Saving the provider's other settings keeps it.

With it off, offboarding someone removed at the provider rests on the session length and on you: sign them out of Geass by letting their
session end, and revoke the tokens they made under **Settings** → **Tokens** (the list says who made each).

## Check people again while they are signed in

A session carries the role, and with organizations the organizations, that the person had when they signed in, and is honoured until it ends. To notice a change sooner, set **Check people again every** (`spec.auth.oidc.refreshMinutes`, 5 to 1440 minutes) in the provider's settings. Geass then asks for the `offline_access` scope, keeps the refresh token the provider gives (sealed with a key made from the session key, in the Secret `geass-sso-refresh`, never shown), and every so often asks the provider again for each person who signed in:

- If the provider no longer honours the token (they signed out there, or were removed), or says the person no longer maps to a role or an organization, their sessions end.
- If what it says of them is not what their session was made with (their role, the organizations or workspaces they are in, a platform claim), their sessions end too, and they sign in again to get what is true now.
- If the provider cannot be reached, nothing is changed: a check that could not be made says nothing about the person. Their session is kept until the provider has been out of reach for **Keep a session when the provider is down for** (`staleGraceMinutes`, 120 by default), and is then ended, because it cannot be vouched for.

It needs a provider that gives refresh tokens for `offline_access`, and one that answers a refresh with a new ID token, which most do; a provider that answers without one still has its sessions ended when it stops honouring the token, but what changed cannot be compared. Off, which is the default, keeps nothing. The people checked are those who signed in through the platform's provider and through any organization's own provider.

## Organizations made by sign-in

With [organizations](/docs/guides/organizations-and-workspaces/) on, only organizations that a platform administrator set up can be signed in to. To let the first sign-in of an organization's administrator make the organization, choose **Make organizations** under **Sign-in** → **Organizations made by sign-in** (`spec.auth.tenancy.autoProvision`). Use it only with a provider in which people you trust make organizations: one that lets anyone make one would let anyone make one here.

- **Pending approval** makes the organization suspended. The person is told it is waiting, and a platform administrator approves it under **Settings** → **Organizations** by turning **Suspended** off. Until then nobody signs in to it.
- **At once, for the ids listed** makes it ready to use, but only for the ids you list (an id as it is, or the start of one followed by `*`), so that it is never every organization the provider has. Open needs the list.

In both, the person has to administer the organization at the provider, and the provider has to say in so many words that their address is verified. At most 20 can be made this way (`max`, to 500), and the ones a platform administrator makes do not count. Nothing but the organization is made: no workspace, no project. A platform administrator is never given an organization they did not ask for.

## Better Auth

Better Auth can be an OpenID Connect provider with its OAuth provider plugin, which needs the `jwt` plugin for its signing keys. The organization plugin gives people a role in an organization, and a small callback puts that role in the token, because Better Auth does not put it there by default. This is the shape of what was tested against Better Auth 1.7:

```ts title="auth.ts (sketch)"
import { betterAuth } from "better-auth";
import { jwt, organization } from "better-auth/plugins";
import { oauthProvider } from "@better-auth/oauth-provider";

export const auth = betterAuth({
  baseURL: "https://auth.example.com",
  plugins: [
    jwt({ disableSettingJwtHeader: true }),
    organization(),
    oauthProvider({
      loginPage: "/sign-in",
      consentPage: "/consent",
      scopes: ["openid", "profile", "email", "offline_access"],
      customIdTokenClaims: async ({ user, scopes }) => ({
        ...(scopes.includes("email") ? { email: user.email, email_verified: user.emailVerified } : {}),
        ...(scopes.includes("profile") ? { name: user.name } : {}),
        ...(await orgClaims(user.id)), // { org_role, organization_id, organization_slug }
      }),
    }),
  ],
});
```

`orgClaims` looks up the person's membership in **the one organization Geass trusts** and returns the role and that organization. Look the member up through `auth.$context.adapter`, because the callback is given the user and the scopes and nothing else, and ask for that organization's id specifically, not for "the person's first membership": a person can belong to several, and anyone can create an organization of their own at Better Auth, where they are its `owner`. Leave the claims out for someone who is not a member of it, and put `organization_id` (or `organization_slug`) in **Only people with** as well, so that a mistake in the callback cannot let a stranger in.

Then:

- **Issuer.** Better Auth's issuer includes its base path, for example `https://auth.example.com/api/auth`. Use that exactly, and check it against `https://auth.example.com/api/auth/.well-known/openid-configuration`.
- **Client.** Create a client with the redirect URI from the page, `token_endpoint_auth_method` `client_secret_basic` (or `client_secret_post`), the scopes `openid profile email`, and consent skipped for a client you run yourself. Better Auth wants a web client's redirect URIs to be `https`.
- **Signing key.** Better Auth signs with EdDSA (Ed25519) unless you change it. Geass accepts it, and ES256, RS256 and the other public-key algorithms. It never accepts a shared-secret algorithm or an unsigned token.
- **Role claim and roles.** The role claim is `org_role`. A member who is an `owner` or an `admin` becomes an admin, and a `member` a viewer:

  ```text
  owner=admin
  admin=admin
  member=viewer
  ```

  To let in one organization only, add `organization_slug=acme` to **Only people with**.
- **Verified email.** Better Auth reports `email_verified: false` until the person has verified the address, and Geass turns away anyone the provider says is unverified. Verify addresses, or leave `email_verified` out of the claims: Geass accepts a provider that does not say.
- **Back-channel logout.** Register `https://geass.example.com/auth/oidc/backchannel-logout` as the client's `backchannel_logout_uri`. Better Auth only accepts a public `https` address there.

## Try it on your own machine

A provider on `localhost`, such as Better Auth during development, is refused by default, as is anything link-local. The operator allows the loopback address only when it starts with `GEASS_ALLOW_LOOPBACK=true`, which `make dashboard-backend` sets. An operator in a cluster leaves it off, because there loopback is the operator's own pod. With it set, an `http://127.0.0.1:3101/api/auth` issuer works for a dashboard on `http://127.0.0.1:8085`, if that redirect URI is registered at the provider. Private network addresses (`10.x`, `192.168.x`) are always allowed, since a provider on the same network is normal.

## What it does not do

- **No roles per project.** Admin and viewer apply to the whole dashboard, as they do for passwords, unless you turn on [organizations and workspaces](/docs/guides/organizations-and-workspaces/): then each organization's people see only its own projects, and a role applies in the place a person works. Teams and organizations at the provider still decide who is in which; Geass reads them from the token and does not keep a list of people.
- **No provisioning.** Geass does not create people ahead of time or keep a list of them. Someone exists here from their first sign-in. Revoke their tokens at **Settings** → **Tokens** if you need to before a logout reaches it.
- **One provider for the platform.** One identity provider decides who is in which organization. An organization can bring [a provider of its own](/docs/guides/organizations-and-workspaces/#an-organizations-own-identity-provider) for its people.
- **The ID token only.** The role and the email are read from the ID token. The provider's userinfo endpoint is not called.
- **Secure endpoints only.** The issuer, and the authorization, token and keys addresses its discovery document lists, have to be `https` (a provider on this machine, with the operator's opt-in below, may use `http`). A document that lists one over plain `http` is refused, because the client secret and the keys travel there.

## If the provider is down or set up wrong

Sign in with an admin's password (**Use an admin password instead**), open **Settings** → **Sign-in** and choose **Switch back to passwords**. Without the dashboard, as the cluster's administrator:

```bash title="Go back to passwords"
sudo k3s kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"auth":{"mode":"local"}}}'
```

The setting is `spec.auth.mode` of the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/). It takes effect at once, and sessions that came through the provider end.

## Related

- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Sign in to the dashboard](/docs/getting-started/sign-in/)
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
- [REST API](/docs/reference/rest-api/)
- [Security](/docs/operations/security/)
- [Troubleshooting](/docs/operations/troubleshooting/)

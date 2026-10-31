---
title: "Sign in with an identity provider"
description: "Let an identity provider such as Better Auth, Keycloak, or Okta decide who signs in to the dashboard and with which role. Set it up, test it, and turn it on."
---

By default, people sign in to the dashboard with a password that an admin sets. That is enough for a few people. It
has no invitations, no sign-in by email, and no organizations or teams. For those features, connect an identity
provider. To keep the projects of each organization apart, also see
[Organizations and workspaces](/docs/guides/organizations-and-workspaces/).

The provider owns the people: who exists, how they prove who they are, and which organization and team they are in.
Geass checks the signed answer of the provider, and turns one claim of that answer into a role: **admin** or
**viewer**.

Any provider that supports OpenID Connect works, for example [Better Auth](https://www.better-auth.com), Keycloak,
Authentik, Zitadel, Auth0, Okta, and Google Workspace. Only the [Better Auth](#better-auth) section is specific to one
provider.

[API tokens](/docs/guides/users-and-tokens/), deploy hooks, and `geass login` work the same with a provider.

:::note
On a Geass that a platform runs for you, with [managed mode](/docs/operations/managed-mode/) on, the platform controls
how people sign in. The settings on this page are read-only.
:::

## What changes and what does not

| | Passwords (the default) | Through an identity provider |
| --- | --- | --- |
| Who can sign in | The users that an admin set | The people that the provider lets in, whose role claim maps to a role |
| Invitations, codes, organizations, teams | None | At the provider |
| Roles | `admin` or `viewer` | `admin` or `viewer`, from a claim of the provider |
| Role changes | Apply to open sessions | Apply at the next sign-in, or sooner if you [check people again](#check-people-again-while-they-are-signed-in). A sign-in lasts an hour by default |
| Removing someone | Delete the user | Remove the person at the provider |
| Tokens and deploy hooks | Unchanged | Unchanged |
| A user with a password | Signs in | Signs in only as an admin. Viewers with a password cannot sign in, and their sessions end |

Admins with a password are the way back in. If the provider is down or set up incorrectly, an admin can still sign in
with a password.

## Set it up

A signed-in admin sets up the provider under **Settings** → **Sign-in**. An API token cannot do this, not even a
**Full access** token.

1. At the provider, register Geass as a client. For the redirect URI, use the **Redirect URI** that the page shows,
   for example `https://geass.example.com/auth/oidc/callback`. Write down the client ID, and the client secret for a
   confidential client.
2. In Geass, complete the fields, and click **Save**:

   | Field | What to enter |
   | --- | --- |
   | **Name** | The name on the button: **Sign in with** this name |
   | **Issuer URL** | The issuer of the provider, exactly as its discovery document names it. It must be an `https` URL. For Better Auth, include `/api/auth` |
   | **Client ID**, **Client secret** | From the provider. For a public client that uses only PKCE, leave the secret empty |
   | **Role claim** | The claim of the ID token that holds the role of the person, for example `org_role`. A dotted path reaches nested claims (`realm_access.roles`). The value can be a string, a list, or a comma-separated string |
   | **Roles** | One for each line: the role at the provider, then `=admin` or `=viewer`. A person with none of these roles cannot sign in. When a person has several roles, the highest role wins |
   | **Only people with** | Optional but important. One for each line, as `claim=value`. For example, `organization_slug=acme` lets in only the members of that organization. Geass compares the whole value. If you set none, everyone that the provider signs in with a mapped role gets in |
   | **Scopes** | The scopes to ask for in addition to `openid`. Empty means `email` and `profile` |
   | **Session length (minutes)** | Minutes before a person goes back to the provider: 5 to 720. The default is 60 |

   **Save** stores the settings. It does not connect to the provider or turn anything on. Geass never shows the client
   secret again. If you change the issuer or the client ID, enter the secret again.
3. Click **Test sign-in**. Sign in at the provider as a person who must be an admin in Geass. You come back to a page
   that shows your email and the role that it maps to, or the reason that Geass refuses you. The test signs nobody in.
4. Click **Use ... to sign in**, and then click **Turn on**. To prevent a lockout, the button works only when all of
   these conditions are true:
   - The settings are complete.
   - A test sign-in with the saved settings passed in the last half hour, as a person who maps to **admin**.
   - An admin with a password exists.

After that, the sign-in page shows **Sign in with ...** first, and **Use an admin password instead** below it.

To change the settings of the provider, first click **Switch back to passwords**. **Remove** deletes the provider and
its secret from Geass. Neither action changes anything at the provider.

If Geass restarts, it forgets the test. Test again before you turn the provider on.

## Roles

The role comes from the provider when the person signs in, through your **Roles** list. Geass does not ask for the
role again until the sign-in ends, after an hour by default. Then the person goes back to the provider. If the person
still has a session there, this step is silent, and Geass gets the current role. If the provider removed the person,
the person cannot sign in.

A person is an admin or a viewer. The `observer` role is for tokens only.

Geass refuses a sign-in with a short message in these cases:

| Message | Why |
| --- | --- |
| Your account has no role here | None of the roles of the person is in your **Roles** list |
| Your account is not one this installation lets in | A claim in **Only people with** is missing or has another value |
| The identity provider did not say what your email address is | The ID token has no `email`. Ask for the `email` scope, or add the claim at the provider |
| The identity provider says your email address is not verified | The token says that `email_verified` is false |
| That sign-in did not start here, or it took too long | The browser that came back is not the browser that left, or the sign-in took more than ten minutes |
| Geass could not complete the sign-in with the identity provider | The provider could not be reached, refused the code, or sent a token that failed the checks |

The dashboard names people by their email address. Geass tells people apart by the issuer and the subject of the
token. So a person whose email changes is still the same person.

## When someone leaves

When you remove a person at the provider, their access ends in three ways. Use all of them:

- **Short sessions.** The next sign-in of the person goes through the provider, and the provider refuses the person.
- **Checking people again.** With [a check every few minutes](#check-people-again-while-they-are-signed-in), Geass
  ends the sessions of a person whom the provider no longer knows.
- **Back-channel logout.** The provider tells Geass immediately when a person signs out or is removed. Give the
  provider the **Back-channel logout URI** that the page shows. Geass then ends every session of that person. By
  default, Geass also revokes the API tokens and deploy hooks that the person made.

Many providers send a back-channel logout only to a public `https` address. So it works for a dashboard on an `https`
address. It does not work for a dashboard on `http://localhost:8085`.

:::caution
Do not make a token or a deploy hook for CI from the account of a person who signs out at the provider. Back-channel
logout revokes it. Make it from an account that does not sign out, and give it an expiry.
:::

**Sign out** in the dashboard ends all sessions of the person in Geass. The person stays signed in at the provider.

### Keep tokens when someone signs out

**Also revoke the tokens and deploy hooks they made** is on by default. Turn it off when the tokens that your CI uses
come from the accounts of people who sign out. You can change it while people sign in through the provider. With it
off, revoke the tokens of a person who leaves yourself, under **Settings** → **Tokens**. The list says who made each
token.

## Check people again while they are signed in

A session keeps the role, and the organizations, that the person had at sign-in. To notice a change sooner, enter a
value in **Check people again every (minutes)**: 5 to 1440. Geass then asks for refresh tokens (`offline_access`), and
asks the provider about each signed-in person at that interval:

- If the provider no longer accepts the token of the person, their sessions end. This happens when the person signed
  out at the provider, or was removed.
- If the role, organizations, or workspaces of the person changed, their sessions end. The person signs in again to
  get the new values.
- If Geass cannot reach the provider, nothing changes until the time in
  **Keep a session when the provider is down for** has passed. The default is 120 minutes. Then the session ends.

This feature needs a provider that gives refresh tokens and returns a new ID token on a refresh. Most providers do.
It is off by default.

## Organizations made by sign-in

With [organizations](/docs/guides/organizations-and-workspaces/) on, people can sign in only to organizations that a
platform administrator made. To let the first sign-in of the administrator of an organization make the organization,
use **Make organizations** under **Organizations made by sign-in**. Use this only with a provider where only people
that you trust make organizations.

- **Pending approval** makes the organization suspended. A platform administrator approves it under **Settings** →
  **Organizations**, by turning off **Waiting for approval**.
- **At once, for the ids listed** makes the organization ready to use, but only for the ids in **Only these ids**. An
  entry is an exact id, or the start of an id followed by `*`.

In both modes, the person must administer the organization at the provider, with a verified address. **Most to make**
limits the organizations made this way. Empty means 20. Geass makes only the organization, with no workspace and no
project.

## Access tokens as a bearer credential

A program can call the API as a person, with the access token that the provider gave that person. For example, an
application that embeds the Geass dashboard can do this. The program sends `Authorization: Bearer <token>`. Geass
records the person as the actor of what the program does.

This feature is **off until you turn it on**. Geass has no dashboard or command-line setting for it yet. See
[Under the hood](#under-the-hood).

The audience keeps a token for one cluster from working on another cluster. Give each cluster its own audience. An
empty audience means the client ID. The feature works only while people sign in through the provider of the
platform, not the provider of an organization.

Geass accepts an access token only when all of these conditions are true. If not, the answer is `401` with
`invalid or expired API token`.

- It is a signed JWT access token with the header `typ` `at+jwt`. Geass never accepts an ID token or an opaque token.
- The provider signed it with a public-key algorithm.
- The issuer is the configured issuer, and the audience includes the configured audience.
- It lives no longer than a sign-in: at most **Session length (minutes)**, 60 by default. Have the provider issue
  short tokens.
- The person is not signed out.

Geass reads the role and organizations from the access token as from an ID token. So the provider must put those
claims in the access token too. A person whose claims map to no access gets `403`.

An access token can do what the person can do. Like an API token, it cannot change sign-in settings, make tokens, or
approve a command-line sign-in.

## Better Auth

Better Auth can be an OpenID Connect provider with its OAuth provider plugin. That plugin needs the `jwt` plugin. The
organization plugin gives people a role in an organization. A callback puts that role in the token. This sketch was
tested with Better Auth 1.7:

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

`orgClaims` finds the membership of the person in **the one organization that Geass trusts**, through
`auth.$context.adapter`. Ask for that organization by its id, not for the first membership of the person. Anyone can
create an organization at Better Auth and be its `owner`. Return no claims for a person who is not a member. Also put
`organization_id` or `organization_slug` in **Only people with**.

Then set these items:

- **Issuer URL.** The issuer of Better Auth includes its base path, for example `https://auth.example.com/api/auth`.
- **Client.** Create a client with the redirect URI from the page, `token_endpoint_auth_method`
  `client_secret_basic`, and the scopes `openid profile email`. Better Auth requires `https` redirect URIs for a web
  client.
- **Role claim and Roles.** The role claim is `org_role`. Map the roles like this:

  ```text
  owner=admin
  admin=admin
  member=viewer
  ```

- **Verified email.** Better Auth reports `email_verified: false` until the person verifies the address. Geass refuses
  such a person. Verify addresses, or leave `email_verified` out of the claims.
- **Back-channel logout.** Register the **Back-channel logout URI** as the `backchannel_logout_uri` of the client.
- **Access tokens.** To use [access tokens](#access-tokens-as-a-bearer-credential), register the cluster as a
  resource, and ask for that resource with the `resource` parameter. Without a resource, Better Auth issues an opaque
  token, which Geass refuses. Add the role and organization claims with `customAccessTokenClaims`. Use the same `sub`
  in both tokens.

## Try it on your own machine

By default, Geass refuses a provider on `localhost`. To allow one, start Geass with `GEASS_ALLOW_LOOPBACK=true`. `make
dashboard-backend` sets this variable. Leave it off in a cluster. With the variable set, an issuer such as
`http://127.0.0.1:3101/api/auth` works for a dashboard on `http://127.0.0.1:8085`. Private network addresses, for
example `10.x` and `192.168.x`, are always allowed.

## What it does not do

- **No roles for each project.** Admin and viewer apply to the whole dashboard, unless you turn on
  [organizations and workspaces](/docs/guides/organizations-and-workspaces/).
- **No list of people.** A person exists in Geass from their first sign-in. To revoke the tokens of a person before a
  logout reaches Geass, revoke them under **Settings** → **Tokens**.
- **One provider for the platform.** An organization can bring
  [its own provider](/docs/guides/organizations-and-workspaces/#an-organizations-own-identity-provider).
- **Claims from tokens only.** Geass reads the role and the email from the ID token. It does not call the userinfo
  endpoint of the provider.
- **Secure addresses only.** The issuer, and the addresses in its discovery document, must use `https`.

## If the provider is down or set up wrong

1. On the sign-in page, click **Use an admin password instead**, and sign in.
2. Open **Settings** → **Sign-in**.
3. Click **Switch back to passwords**.

The change applies immediately, and the sessions that came through the provider end. If you cannot use the dashboard,
Geass has no other way back yet. See [Under the hood](#under-the-hood).

## Under the hood

- The settings are under `spec.auth` of the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/):
  `mode`, `oidc.refreshMinutes`, `oidc.staleGraceMinutes`, `oidc.revokeTokensOnLogout`, `oidc.accessTokens`, and
  `tenancy.autoProvision`.
- The client secret is in the Secret `platform-auth`. The refresh tokens are sealed in the Secret `geass-sso-refresh`.
- The operator log says why a sign-in failed with "Geass could not complete the sign-in with the identity provider".
- To accept the access tokens of the provider, run this command on the server. Give each cluster its own audience:

  ```bash title="Accept the access tokens of the provider"
  sudo k3s kubectl -n geass-system patch geassplatformconfig platform --type merge \
    -p '{"spec":{"auth":{"oidc":{"accessTokens":{"enabled":true,"audience":"https://geass.example.com/api"}}}}}'
  ```

- To go back to passwords when you cannot use the dashboard, run this command on the server:

  ```bash title="Go back to passwords"
  sudo k3s kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"auth":{"mode":"local"}}}'
  ```

## Related

- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Sign in to the dashboard](/docs/getting-started/sign-in/)
- [REST API](/docs/reference/rest-api/)
- [Security](/docs/operations/security/)
- [Troubleshooting](/docs/operations/troubleshooting/)

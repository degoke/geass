---
title: "Sign in with an identity provider"
description: "Let an identity provider such as Better Auth, Keycloak, or Okta decide who signs in to the dashboard and with which role. Set it up, test it, and turn it on."
---

By default, the people who can sign in to the dashboard are the users in a Kubernetes Secret, each with a password. That model is enough for a few people. It has no invitations, no sign-in by email code or link, and no organizations or teams. If you want those features, connect an identity provider. To keep the projects of each organization apart, also see [organizations and workspaces](/docs/guides/organizations-and-workspaces/).

The provider owns the people: who exists, how they prove who they are, who was invited, and which organization and team they are in. Geass does not add any of that. Geass verifies the signed answer of the provider, and turns one claim of that answer into one of its two roles, **admin** or **viewer**.

Any provider that speaks OpenID Connect works: [Better Auth](https://www.better-auth.com), Keycloak, Authentik, Zitadel, Auth0, Okta, Google Workspace, and others. Nothing on this page is specific to one provider, except the recipe for Better Auth near the end.

Programs are not affected. [API tokens](/docs/guides/users-and-tokens/), deploy hooks, and `geass login` work the same in both modes. A signed-in admin makes a token, and the token is tied to the person who made it.

:::note
On a Geass that a platform runs for you, with [managed mode](/docs/operations/managed-mode/) on, the platform controls how people sign in.
The settings on this page are read-only, and the API refuses to change them (`403`, `code` `managed`).
:::

## What changes and what does not

| | Passwords from the Secret (the default) | Through an identity provider |
| --- | --- | --- |
| Who can sign in | The users in `geass-dashboard-auth` | Whoever the provider lets in and whose role claim maps to a role |
| Invitations, codes, organizations, teams | None | At the provider |
| Roles | `admin` or `viewer`, set in the Secret | `admin` or `viewer`, from a claim of the token of the provider |
| Role changes | Apply to open sessions | Apply when the person next signs in, or within minutes if you [check people again](#check-people-again-while-they-are-signed-in). A sign-in lasts an hour by default |
| Removing someone | Delete the entry | Remove the person at the provider |
| Tokens and deploy hooks | Unchanged | Unchanged |
| A user with a password in the Secret | Signs in | Signs in only if the user is an admin. The password of a viewer is refused, and when you turn on the provider, the open sessions of viewers end |

The admins with a password are the way back in. While the provider decides who signs in, a user in the Secret who is an admin can still sign in with a password. So a provider that is down, or set up incorrectly, does not lock everyone out.

## Set it up

A signed-in admin sets up the provider under **Settings** → **Sign-in**. An API token cannot do this, not even an admin token. The reason is that something that outlives the access of a person must not be able to change who can sign in.

1. At the provider, register Geass as a client. The provider asks for a redirect URI. Use the address that the page shows. This address is the own address of the dashboard with `/auth/oidc/callback`, for example `https://geass.example.com/auth/oidc/callback`.
   - After the domain of the dashboard is set up and verified, the redirect URI is the address where the dashboard is published. This is true whatever host a request arrives with. So the redirect list of the provider never has to trust a host that an attacker chose.
   - Before that, the redirect URI is the address where you opened the page. This applies when there is no domain yet, or when you reach the dashboard on `localhost`. You must then sign in at that address.

   Write down the client ID. For a confidential client, also write down the client secret.
2. In Geass, complete the fields on the page and click **Save**:

   | Field | What to put |
   | --- | --- |
   | Name | The text of the button: **Sign in with** this name |
   | Issuer URL | The issuer of the provider, exactly as its discovery document names itself. It must be an `https` URL. Geass reads `<issuer>/.well-known/openid-configuration` |
   | Client ID, Client secret | From the provider. For a public client that uses only PKCE, leave the secret empty |
   | Role claim | The claim of the ID token that holds the role of the person at the provider, for example `org_role`. A dotted path reaches into nested claims (`realm_access.roles`). Its value can be a string, a list, or a comma-separated string. Better Auth uses a comma-separated string for a member who has several roles |
   | Roles | One per line: the role at the provider, then `=admin` or `=viewer`. A person who has none of these roles cannot sign in. When a person has several roles, the highest role wins |
   | Only people with | Optional but important. One per line, as `claim=value`. `organization_slug=acme` lets in only the members of that organization. The claim can be a single value or a list that contains the value. Geass compares the value whole and never splits text at commas, so an organization called `x, Acme` is not `Acme`. If you set none, everyone that the provider signs in with a mapped role gets in. So add one whenever anyone can sign up at the provider |
   | Scopes | The scopes to ask for in addition to `openid`. Empty means `email` and `profile` |
   | Session length | Minutes before a person goes back to the provider (5 to 720, default 60) |

   When you save, Geass stores the settings. Geass does not connect to the provider, and it does not turn anything on. Geass keeps the result of a test in memory, so after an operator restart you must test again.

   The client secret goes in the Secret `platform-auth`, and Geass never shows it again. Geass keeps the secret only for the provider and the client that it was saved for. If you change the issuer or the client ID, Geass asks you for the secret again. So the secret of one provider can never go to another provider.
3. Click **Test sign-in**. Geass sends you to the provider. Sign in as a person who would be an admin in Geass. You come back to a page that says what Geass found: your email and the role that it maps to, or the reason that Geass would refuse you. The test does not sign anyone in.
4. Click **Use ... to sign in**. This action needs all of these conditions, so that a typo cannot lock everyone out:
   - The settings are complete.
   - A test sign-in with exactly the saved settings succeeded in the last half hour, as a person who maps to **admin**.
   - A user in the Secret is an admin with a password.

After that, the sign-in page shows **Sign in with ...** first, and keeps **Use an admin password instead** below it. The browser remembers a person who signs in this way, and that person goes directly to the provider next time.

To change the settings of the provider, first click **Switch back to passwords**. While people sign in through the provider, you cannot edit the settings. **Remove** deletes the provider and its secret from Geass. Neither action changes anything at the provider.

## Roles

The role is the one that the provider gave when the person signed in, mapped through your **Roles** list. Geass does not look up the role again. That is why a sign-in lasts a limited time, an hour by default. When the sign-in ends, the person goes back to the provider. If the person still has a session there, this step is silent and picks up the current role of the person. If the person was removed or signed out at the provider, the person must sign in, and cannot.

`observer` is a token role only. A person is an admin or a viewer.

Geass refuses a sign-in, with a short message on the sign-in page, in these cases:

| Message | Why |
| --- | --- |
| Your account has no role here | None of the roles of the person at the provider is in your **Roles** list |
| Your account is not one this installation lets in | A claim in **Only people with** is missing or has another value |
| The identity provider did not say what your email address is | The ID token has no `email`. Ask for the `email` scope, or add the claim at the provider |
| The identity provider says your email address is not verified | The token says that `email_verified` is false. Geass believes a provider that says no, and accepts a provider that does not say |
| That sign-in did not start here, or it took too long | The browser that came back is not the browser that left, or the sign-in took more than ten minutes |
| Geass could not complete the sign-in with the identity provider | The provider could not be reached, refused the code, or sent a token that failed the checks. The operator log says which |

The dashboard names people by their email address, also in what they make. Geass tells people apart by the issuer and the subject, which a provider does not reuse. So a person whose email changes is still the same person.

## When someone leaves

When you remove a person at the provider, their access ends in three ways. Use all of them.

- **Short sessions.** The next sign-in of the person, at most one session length later, goes through the provider, and the provider refuses the person.
- **Checking people again.** With [a check every few minutes](#check-people-again-while-they-are-signed-in), Geass asks the provider if each signed-in person is still one of its people. Geass ends the sessions of a person who is not, and it does not wait for the session to end.
- **Back-channel logout.** When a person signs out at the provider or is removed, the provider can tell Geass immediately. Give the provider the **Back-channel logout URI** that the page shows, `https://geass.example.com/auth/oidc/backchannel-logout`. Geass then ends every session of that person, in every browser. Unless you turn it off, as described later in this section, Geass also revokes the API tokens and deploy hooks that the person made. A provider cannot say if a person signed out or was removed. So with this setting on, both events revoke the tokens. **Do not make an API token or deploy hook that CI depends on from the account of a person who signs out.** Make it, with an expiry, from an account that does not sign out.

Without back-channel logout, or with the switch described later off, a token that a person made while they had access continues to work. It works until it expires, or until you revoke it under **Settings** → **Tokens**. Many providers require that the logout address is a public `https` address that they can reach. So back-channel logout works for a dashboard published on a domain. It does not work for a dashboard that you reach only through an SSH tunnel or on `localhost`.

Geass accepts a logout token only when all of these conditions are true:

- The provider signed the token.
- The token was issued for this client.
- The token has not expired, and it is less than five minutes old.
- The token carries the logout event, an identifier, and a subject.
- The token has no nonce.

A token that names only a session, with no subject, cannot be matched to a person, and Geass refuses it. Geass answers a token that it has already acted on, and ignores it. So a replay does not sign anyone out of a later session. Geass slows down a source that continues to send bad tokens.

**Sign out** in the dashboard ends all sessions of that person in Geass, in every browser. It does not sign the person out of the provider, where they stay signed in. To end that session, sign out at the provider.

### Keep tokens when someone signs out

Under the back-channel logout address, **Also revoke the tokens and deploy hooks they made** is on by default. Turn it off when both of these are true:

- The tokens and hooks that your CI uses were made from the accounts of people who sign out at the provider.
- You prefer to revoke these tokens and hooks manually when someone leaves.

A logout then ends the sessions of the person, and leaves what they made.

This switch is the setting `spec.auth.oidc.revokeTokensOnLogout`. Geass saves it immediately. Unlike the other settings of the provider,
you can change it while people sign in through the provider, because it does not change who can sign in. When you save the other settings
of the provider, Geass keeps this setting.

With this setting off, the offboarding of a person removed at the provider depends on the session length and on you. To sign the person
out of Geass, let their session end. Then revoke the tokens that they made under **Settings** → **Tokens**. The list says who made each
token.

## Check people again while they are signed in

A session carries the role that the person had when they signed in. With organizations, the session also carries the organizations of the person. Geass honors the session until it ends. To notice a change sooner, set **Check people again every** in the settings of the provider. The field is `spec.auth.oidc.refreshMinutes`, 5 to 1440 minutes. Geass then does these things:

- Geass asks for the `offline_access` scope.
- Geass keeps the refresh token that the provider gives. Geass seals the token with a key made from the session key, keeps it in the Secret `geass-sso-refresh`, and never shows it.
- At that interval, Geass asks the provider again about each person who signed in.

The result of each check is one of these:

- If the provider no longer honors the token, the sessions of the person end. This happens when the person signed out at the provider, or was removed. The sessions also end if the provider says that the person no longer maps to a role or an organization.
- If what the provider says about the person is not what their session was made with, their sessions also end. This covers their role, the organizations or workspaces they are in, and a platform claim. The person then signs in again to get the current values.
- If Geass cannot reach the provider, nothing changes: a check that could not be made says nothing about the person. Geass keeps the session until the provider has been out of reach for **Keep a session when the provider is down for** (`staleGraceMinutes`, 120 by default). Then Geass ends the session, because nothing can vouch for it.

This feature needs a provider that gives refresh tokens for `offline_access`, and that answers a refresh with a new ID token. Most providers do. If a provider answers without a new ID token, Geass still ends the sessions when the provider stops honoring the token. But Geass cannot compare what changed. Off is the default, and then Geass keeps nothing. Geass checks the people who signed in through the provider of the platform, and through the own provider of any organization.

## Organizations made by sign-in

With [organizations](/docs/guides/organizations-and-workspaces/) on, people can sign in only to organizations that a platform administrator set up. To let the first sign-in of the administrator of an organization make the organization, select **Make organizations** under **Sign-in** → **Organizations made by sign-in** (`spec.auth.tenancy.autoProvision`). Use this setting only with a provider in which people that you trust make organizations. With a provider that lets anyone make an organization, anyone could make one here.

- **Pending approval** makes the organization suspended. Geass tells the person that the organization is waiting for approval. A platform administrator approves the organization under **Settings** → **Organizations** by turning off **Suspended**. Until then, nobody signs in to the organization.
- **At once, for the ids listed** makes the organization ready to use, but only for the ids that you list. An entry is an exact id, or the start of an id followed by `*`. So this setting never applies to every organization of the provider. Open needs the list.

In both modes, the person must administer the organization at the provider. The provider must also say explicitly that the address of the person is verified. At most 20 organizations can be made this way (`max`, up to 500). The organizations that a platform administrator makes do not count. Geass makes only the organization: no workspace, and no project. Geass never gives a platform administrator an organization that they did not ask for.

## Access tokens as a bearer credential

A program can call the API as the person, with the access token that the provider issued to that person. A page that is signed in at the provider itself can do the same, for example an application that embeds the Geass dashboard. Send `Authorization: Bearer <token>`. For the WebSocket of a terminal, use the subprotocol `geass.bearer.<token>`. Geass makes and keeps no Geass token. What Geass records, for example the actor of a deploy, a terminal, or a change, is the person: their address, or their subject when the token carries no address.

This feature is **off until you turn it on**, with `spec.auth.oidc.accessTokens` in the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/). It does something only while people sign in through the provider. It is off by default because it is another way in. A cluster that is updated keeps exactly the credentials that it accepted before, and an operator chooses to add this credential and its audience. The setting is not on the settings page, and when you save the provider there, Geass keeps the setting. It is for the provider of the platform. The [own provider](/docs/guides/organizations-and-workspaces/#an-organizations-own-identity-provider) of an organization has no access tokens.

```yaml title="Accept the provider's access tokens"
spec:
  auth:
    oidc:
      accessTokens:
        enabled: true
        audience: https://geass.example.com/api   # empty means the client ID
```

Geass tries a credential as an access token only when the credential does not start with one of the Geass prefixes (`gsw_`, `gsp_`, `gsh_`). Geass looks up a credential with one of these prefixes as an API token. Geass accepts an access token only when all of these conditions are true. If any condition is false, Geass refuses the token with `401 invalid or expired API token`. The answer never says which condition failed.

- It is a **JWT access token in the form of RFC 9068**: a signed JWT whose header has `typ` `at+jwt` (or `application/at+jwt`). An ID token is signed by the same keys and is addressed to the same client. So this header is what tells an ID token from an access token. Geass never accepts an ID token here. Geass also does not accept a token with a `nonce`, `at_hash`, or `c_hash`, or a `token_use` that is not `access`. Geass does not accept an opaque token, that is, a token that is not a JWT. Geass does not ask the provider about an opaque token.
- It is signed by one of the keys of the issuer. Geass reads these keys from the `jwks_uri` of the discovery document of the provider. These are the same keys as for ID tokens, with the same limits on how often Geass fetches them again. Geass accepts only public-key algorithms (`EdDSA`, `ES256`/`384`/`512`, `RS256`/`384`/`512`, `PS256`/`384`/`512`). Geass never accepts `none` or the HMAC algorithms.
- `iss` is the configured issuer. `aud`, a string or a list, contains the **audience**: `accessTokens.audience`, or the client ID when that field is empty. The audience keeps a token for one cluster from working on another cluster of the same provider. So give each cluster its own audience.
- `sub`, `iat`, and `exp` are present. Geass checks `exp`, `nbf`, and `iat` with an allowed clock difference of 30 seconds.
- **It does not live longer than a sign-in**. `exp` minus `iat` is at most `sessionMinutes`, 60 by default. The claims of the token are those of the moment when the provider issued it, and Geass does not ask the provider again. So this is the longest time that the token continues to honor a changed role or a removed person. Have the provider issue short tokens.
- The person is not signed out. A sign-out in Geass, or a back-channel logout from the provider, also ends the tokens of the person issued before it, as it ends their sessions.

The claims then become a session **exactly as an ID token's do**. For one organization of the provider, Geass uses the role claim, `roleMap`, and `requireClaim`. When [organizations](/docs/guides/organizations-and-workspaces/) are on, Geass uses the organizations claim and the platform claim. So an access token gives nothing that a sign-in does not give. Geass reads those claims from the access token, so the provider must put them in it. An access token does not carry these claims unless the provider is told to add them. See [Better Auth](#better-auth).

Geass refuses a token whose claims map to no role, or to no organization that Geass knows, with `403`. The answer names the short reason code (`no_role`, `required_claim`, `no_org`, `email_unverified`). The `email` claim is optional in an access token. Geass refuses a token that carries an address that the provider says is not verified. With organizations, a token works in the first place that the person would start in at sign-in. A token never makes an organization, whatever `autoProvision` says.

An access token can do what a signed-in person can do: an admin role changes things, and a viewer role reads. Like an API token, an access token is refused on every route that decides who can sign in or that makes a credential. These routes are the sign-in settings, tokens, the approval of a device code, and the change of where a session works. Geass closes a stream or a terminal that the token opened when the token expires, or when the person is signed out.

## Better Auth

Better Auth can be an OpenID Connect provider with its OAuth provider plugin. That plugin needs the `jwt` plugin for its signing keys. The organization plugin gives people a role in an organization. A small callback puts that role in the token, because Better Auth does not put it there by default. This is the shape of the configuration that was tested against Better Auth 1.7:

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

`orgClaims` looks up the membership of the person in **the one organization Geass trusts**, and returns the role and that organization. Look up the member through `auth.$context.adapter`, because the callback gets only the user and the scopes. Ask for the id of that organization specifically, not for "the first membership of the person". A person can belong to several organizations. Also, anyone can create their own organization at Better Auth, where they are its `owner`. Leave the claims out for a person who is not a member of that organization. Also put `organization_id` (or `organization_slug`) in **Only people with**, so that a mistake in the callback cannot let a stranger in.

Then configure these items:

- **Issuer.** The issuer of Better Auth includes its base path, for example `https://auth.example.com/api/auth`. Use that value exactly, and check it against `https://auth.example.com/api/auth/.well-known/openid-configuration`.
- **Client.** Create a client with the redirect URI from the page, `token_endpoint_auth_method` `client_secret_basic` (or `client_secret_post`), and the scopes `openid profile email`. For a client that you run yourself, skip consent. Better Auth requires `https` redirect URIs for a web client.
- **Signing key.** Better Auth signs with EdDSA (Ed25519) unless you change it. Geass accepts EdDSA, ES256, RS256, and the other public-key algorithms. Geass never accepts a shared-secret algorithm or an unsigned token.
- **Role claim and roles.** The role claim is `org_role`. A member who is an `owner` or an `admin` becomes an admin, and a `member` becomes a viewer:

  ```text
  owner=admin
  admin=admin
  member=viewer
  ```

  To let in only one organization, add `organization_slug=acme` to **Only people with**.
- **Verified email.** Better Auth reports `email_verified: false` until the person has verified the address. Geass refuses anyone that the provider says is unverified. Verify addresses, or leave `email_verified` out of the claims. Geass accepts a provider that does not say.
- **Back-channel logout.** Register `https://geass.example.com/auth/oidc/backchannel-logout` as the `backchannel_logout_uri` of the client. Better Auth accepts only a public `https` address there.
- **Access tokens.** To use [the access token as a bearer credential](#access-tokens-as-a-bearer-credential), do these steps:
  1. Register the cluster as a resource. The resource is the audience.
  2. When the token is requested, ask for that resource with the `resource` parameter (RFC 8707).

  Better Auth then issues a JWT access token (`typ` `at+jwt`). Its `aud` is the resource, `client_id` and `azp` are the client, and `sid` is the session. Without a resource, Better Auth issues an opaque token, which Geass does not accept. When the `openid` scope was granted, Better Auth adds the address of its userinfo endpoint to `aud`. Geass ignores that address.

  The role and organization claims are not in an access token unless you add them with `customAccessTokenClaims`. That option takes the same shape as `customIdTokenClaims`, and it can also return the `email` and `email_verified` claims. The `sub` of the access token is the id of the user. Geass names a person by the issuer and the `sub` of the ID token. So use the same `sub` for both tokens, and do not use pairwise subjects. If you do, the tokens and the sign-ins of a person are two different people.

## Try it on your own machine

By default, Geass refuses a provider on `localhost`, for example Better Auth during development. Geass also refuses anything link-local. The operator allows the loopback address only when the operator starts with `GEASS_ALLOW_LOOPBACK=true`. `make dashboard-backend` sets this variable. An operator in a cluster leaves it off, because in a cluster the loopback address is the pod of the operator itself. With the variable set, an `http://127.0.0.1:3101/api/auth` issuer works for a dashboard on `http://127.0.0.1:8085`, if that redirect URI is registered at the provider. Private network addresses (`10.x`, `192.168.x`) are always allowed, because a provider on the same network is normal.

## What it does not do

- **No roles per project.** Admin and viewer apply to the whole dashboard, as they do for passwords. The exception is when you turn on [organizations and workspaces](/docs/guides/organizations-and-workspaces/). Then the people of each organization see only the projects of that organization, and a role applies in the place where a person works. Teams and organizations at the provider still decide who is in which. Geass reads them from the token and does not keep a list of people.
- **No provisioning.** Geass does not create people ahead of time, or keep a list of them. A person exists in Geass from their first sign-in. If you must revoke the tokens of a person before a logout reaches Geass, revoke them at **Settings** → **Tokens**.
- **One provider for the platform.** One identity provider decides who is in which organization. An organization can bring [a provider of its own](/docs/guides/organizations-and-workspaces/#an-organizations-own-identity-provider) for its people.
- **The ID token only, or a JWT access token.** At sign-in, Geass reads the role and the email from the ID token, and does not call the userinfo endpoint of the provider. Geass reads [Access tokens](#access-tokens-as-a-bearer-credential) the same way, from their own claims, and only when they are JWTs.
- **Secure endpoints only.** The issuer, and the authorization, token, and keys addresses that its discovery document lists, must be `https`. A provider on this machine can use `http`, with the opt-in of the operator below. Geass refuses a document that lists one of these addresses over plain `http`, because the client secret and the keys travel there.

## If the provider is down or set up wrong

1. Sign in with the password of an admin: click **Use an admin password instead**.
2. Open **Settings** → **Sign-in**.
3. Click **Switch back to passwords**.

If you cannot use the dashboard, run this command as the administrator of the cluster:

```bash title="Go back to passwords"
sudo k3s kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"auth":{"mode":"local"}}}'
```

The setting is `spec.auth.mode` of the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/). The change takes effect immediately, and the sessions that came through the provider end.

## Related

- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Sign in to the dashboard](/docs/getting-started/sign-in/)
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
- [REST API](/docs/reference/rest-api/)
- [Security](/docs/operations/security/)
- [Troubleshooting](/docs/operations/troubleshooting/)

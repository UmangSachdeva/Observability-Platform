# Observability Platform API

A small Go HTTP server for receiving application error reports. Accepted
events are stored in MongoDB.

## Run

```sh
go run ./cmd/server
```

The server listens on `:8080`. Set `HTTP_ADDR` to change the address. It expects
MongoDB at `mongodb://localhost:27017` by default. Start MongoDB locally before
the API, then configure the connection with:

- `MONGODB_URI` (default `mongodb://localhost:27017`)
- `MONGODB_DATABASE` (default `observability`)
- `MONGODB_COLLECTION` (default `errors`)
- `MONGODB_KEYS_COLLECTION` (default `api_keys`)
- `MONGODB_USERS_COLLECTION` (default `users`)
- `MONGODB_ACCOUNTS_COLLECTION` (default `accounts`)
- `MONGODB_INVITES_COLLECTION` (default `account_invites`)
- `MONGODB_PROJECTS_COLLECTION` (default `projects`)
- `API_KEY_ENCRYPTION_KEY` (required; base64 encoded 32-byte AES key)
- `USER_JWT_SECRET` (required; base64 encoded key of at least 32 bytes)

The server verifies the MongoDB connection at startup and exits if MongoDB is
unavailable.

## Endpoints

- `GET /healthz` returns `{"status":"ok"}`.
- `POST /v1/accounts/{accountID}/projects/{projectID}/keys` creates API credentials for a project (user JWT required).
- `GET /v1/auth/keys` lists the caller's API keys (user JWT required). `POST /v1/auth/keys` now returns `410 Gone`.
- `POST /v1/auth/signup` creates a user account.
- `POST /v1/auth/login` verifies account credentials and returns `account_id` with access and refresh JWTs.
- `POST /v1/accounts/{accountID}/invites` creates an account invitation (admin JWT required).
- `POST /v1/invites/accept` accepts an invitation for a new or already registered user (user JWT required).
- `GET /v1/accounts/{accountID}` returns account details for a member (user JWT required).
- `GET /v1/accounts/{accountID}/projects` lists account projects (user JWT required).
- `POST /v1/accounts/{accountID}/projects` creates an account project (user JWT required).
- `DELETE /v1/accounts/{accountID}/members/me` removes the caller from the account (user JWT required).
- `GET /v1/invites` lists pending invitations for the caller's email (user JWT required).
- `PATCH /v1/invites/{inviteID}` snoozes or un-snoozes a pending invitation (user JWT required).
- `POST /v1/errors` accepts one JSON error event for the API key's project and returns `202 Accepted`.
- `GET /v1/accounts/{accountID}/projects/{projectID}/errors?limit=100` lists a project's latest events (user JWT required, maximum limit: 500).

Generate an encryption key before starting the server:

```sh
export API_KEY_ENCRYPTION_KEY="$(openssl rand -base64 32)"
```

Create a user account, log in, create a project, and use the returned bearer
token to create API credentials for that project:

```sh
curl -X POST http://localhost:8080/v1/auth/signup \
  -H 'Content-Type: application/json' \
  -d '{"username":"sam_dev","email":"sam@example.com","password":"a-long-password"}'

curl -X POST http://localhost:8080/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"login":"sam@example.com","password":"a-long-password"}'

curl -X POST http://localhost:8080/v1/accounts/<account_id>/projects \
  -H 'Authorization: Bearer <user-jwt>' \
  -H 'Content-Type: application/json' \
  -d '{"name":"checkout"}'

curl -X POST http://localhost:8080/v1/accounts/<account_id>/projects/<project_id>/keys \
  -H 'Authorization: Bearer <user-jwt>' \
  -H 'Content-Type: application/json' \
  -d '{"client_name":"checkout-production"}'
```

Every API key belongs to one project. Any member of the project's account can
create one. The response contains `access_key`, `secret_key`, `account_id` and
`project_id`. Events sent with the key are stored under that project, so the
client never sends a project ID when reporting errors. Save the secret when
returned; it is only returned at creation time and is encrypted in MongoDB.
Keep the encryption key stable between server restarts; changing it makes
existing client secrets unusable. Key creation is rate limited to five requests
per source IP per minute.

`GET /v1/auth/keys` (user JWT) lists the caller's keys, newest first (at most
200), as `{"keys":[{"access_key":"ak_…","client_name":"…","account_id":"…","project_id":"…","created_at":"…"}]}`.
Secrets are never returned after creation.

Only client API-key signatures authorize `POST /v1/errors`; user bearer JWTs do
not. Keys created before keys were linked to projects still pass the signature
check but are refused with `403 key_not_scoped`; create a new key under a
project. Every `POST /v1/errors` call must include `X-Access-Key`, `X-Timestamp`, and
`X-Signature` headers. `X-Timestamp` is Unix time in seconds and must be within
five minutes of server time. The signature is the lowercase hex HMAC-SHA256 of
the following UTF-8 string, using the secret key as the HMAC key:

```text
<timestamp>\n<HTTP method>\n<URL path and query>\n<lowercase hex SHA-256 of exact body bytes>
```

For an empty request body, hash the empty byte string. The URL path and query
must match exactly, including the query string. Requests with a stale timestamp
or invalid signature receive `401 Unauthorized`.

User signup accepts `username`, `email`, and `password`. Usernames are
case-insensitive, 3–32 characters, and may contain letters, numbers, dots,
dashes, and underscores. Passwords must be 12–72 bytes. Passwords are stored
as bcrypt hashes, not reversibly encrypted. Signup and login return a 24-hour
`access_token` and a 30-day `refresh_token`, both JWTs. Only access tokens are
accepted by the user authentication middleware. Keep the JWT signing secret
stable between server restarts. Generate it with:

```sh
export USER_JWT_SECRET="$(openssl rand -base64 32)"
```

Signup and login are rate limited to ten combined requests per source IP per
minute. The JWT is issued for account authentication; error ingestion continues
to use the signed client access/secret key scheme.

The login response includes `account_id`, so a returning user can load the same
account details and projects without relying on a previously saved signup
response. For users in multiple accounts, login selects an account they own;
if they own none, it selects their oldest membership.

Each signup also creates an account and adds the new user as its admin; the
signup response includes `account_id`. An account admin can invite an email
address to that account. The response contains a one-time `invite_token` that
must be shared with the invitee; it expires after seven days and only an
authenticated user whose email matches the invited email can accept it. This
works both for someone who already has an account and for someone who signs up
after receiving the invitation. Invite tokens are stored as hashes. Email
delivery is not connected yet, so the inviting user must deliver the token to
the invitee.

Accounts have two roles: `admin` and `member`. Only admins can invite people;
they may assign either role in the invite request. If `role` is omitted, it
defaults to `member`.

To invite someone, send `POST /v1/accounts/<account_id>/invites` with an admin
bearer JWT and `{"email":"person@example.com","role":"member"}`. Share the
returned `invite_token` with them. If they already registered, they log in
with the invited email and accept using their existing access JWT. If they are
new, they first sign up with the invited email, then use the access JWT from
signup. In either case, they call `POST /v1/invites/accept` with
`{"invite_token":"<token from the invite response>"}`. The response contains
the joined `account_id` so the client can load that account's projects and
details immediately.

Any member can leave an account with `DELETE /v1/accounts/<account_id>/members/me`
and their user JWT. The response is `{"account_id":"<account_id>","status":"left"}`.
Admins can leave too, but only while at least one other admin remains in the
account; otherwise the request returns `409` with code `last_admin`. Leaving does
not delete the user: they can still log in and accept new invitations. If a user
has left every account, the login response omits `account_id`. Rejoining the same
account needs a new invitation.

A signed-in user can see invitations sent to their email with `GET /v1/invites`.
The list contains invitations that are not accepted, not expired, and not
currently snoozed, newest first (at most 100). Add `?include_snoozed=true` to
also include snoozed ones. Each item has `id`, `account_id`, `account_name`,
`email`, `role`, `invited_by`, `created_at`, `expires_at` and, when set,
`snoozed_until`. The list does not contain the `invite_token`. To accept an
invitation from this list, call `POST /v1/invites/accept` with
`{"invite_id":"<id>"}` instead of a token; the invitation must still be for the
caller's email. Send exactly one of `invite_token` or `invite_id`.

To snooze an invitation, send `PATCH /v1/invites/<invite_id>` with
`{"snoozed_until":"2026-11-12T09:00:00Z"}`. The time must be in the future and
no later than the invitation's `expires_at`. The invitation disappears from
`GET /v1/invites` until that time. Send `{"snoozed_until":null}` to show it
again immediately. Snoozing does not change the expiry, and a snoozed
invitation can still be accepted. Invitations for another email address return
`404`, accepted ones `409`, and expired ones `410`.

Account members can list projects with `GET /v1/accounts/<account_id>/projects`
and create one with `POST /v1/accounts/<account_id>/projects`, using their user
JWT. Project data is stored in the separate `projects` collection and scoped to
its account. Each account can have only one project with a given name.

Signup request:

```json
{"username":"sam_dev","email":"sam@example.com","password":"a-long-password"}
```

Login request (use either username or email in `login`):

```json
{"login":"sam@example.com","password":"a-long-password"}
```

Example event:

```sh
EVENT_BODY='{"service":"checkout-api","environment":"production","level":"error","message":"payment request failed","stack_trace":"..."}'
```

Send those exact body bytes with the three signature headers described above.

Events persist across server restarts. MongoDB documents are stored in the
configured collection with their `account_id` and `project_id`, sorted by
receipt time when listed. Members of the account read them with
`GET /v1/accounts/<account_id>/projects/<project_id>/errors` and their user JWT.
Events stored before this change have no project and are not returned.

## Layers

- `controller` owns HTTP routes and request/response DTOs.
- `validation` validates incoming DTOs.
- `service` applies defaults and business rules, then uses `mapper` to convert
  transport DTOs to persisted entities.
- `repository/interfaces` defines reusable MongoDB operations such as
  `InsertOne`, `FindOne`, `Find`, `UpdateOne`, and `DeleteOne`, plus the
  entity-specific repository contracts.
- `repository/mongo` implements those interfaces with MongoDB operations and
  contains the entity-specific error-event, API-key, user, account, and invite
  repositories.
- `server` wires the layers and configures the HTTP server.

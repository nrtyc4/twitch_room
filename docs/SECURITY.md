# Security Documentation

## Content Security Policy (CSP)

### Implementation
The public share page (`/u/`) now utilizes a tailored Content Security Policy to allow necessary inline styles and scripts while preventing unauthorized execution.

**Current Policy for `/u/` routes (set by `share.go`, overrides the middleware default):**
`default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' https://static-cdn.jtvnw.net https://*.twitch.tv data:; connect-src 'self'`

The middleware's `/u/` branch that allowed `'unsafe-inline'` has been removed; the share page loads its
CSS/JS from `/extension/` and needs no inline allowance.

### Security Measures

#### 1. CSP Strengthening
By restricting `script-src` and `style-src` and explicitly allowing only `unsafe-inline` where absolutely necessary (for the self-contained share page), we significantly reduce the risk of Cross-Site Scripting (XSS) attacks.

#### 2. Input Sanitization
Enhanced validation of User IDs and channel names to ensure identifiers conform to expected patterns.

#### 3. Data Minimization
Implemented the `DeleteUser` functionality to allow for complete purging of user data upon logout or account deletion, adhering to data privacy best practices.

#### 4. Rate Limiting
The `RateLimiter` now includes periodic cleanup of stale entries to prevent memory exhaustion attacks.

#### 5. OAuth state secret rotation
`OAUTH_STATE_SECRET` (or `TWITCH_ROOM_AUTH_TOKEN` as a fallback) signs the short-lived OAuth state
cookie used by `/auth/twitch`. To rotate without breaking in-flight logins:

1. Generate a new secret: `openssl rand -hex 32`.
2. Deploy it as `OAUTH_STATE_SECRET` on **all** instances at once (the value must be shared across instances).
3. In-flight logins issued under the old secret fail verification and the user simply retries; no data is lost.
4. For a zero-downtime rotation, accept both old and new secrets for one TTL window (900s) by keeping the previous value in `OAUTH_STATE_SECRET_PREVIOUS` (not yet implemented — track as future work) before removing it.
5. Never log or commit the secret; it is read from the environment only.

# Security Documentation

## Content Security Policy (CSP)

### Implementation
The public share page (`/u/`) now utilizes a tailored Content Security Policy to allow necessary inline styles and scripts while preventing unauthorized execution.

**Current Policy for `/u/` routes:**
`default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; img-src 'self' https://static-cdn.jtvnw.net https://*.twitch.tv data:; connect-src 'self'`

### Security Measures

#### 1. CSP Strengthening
By restricting `script-src` and `style-src` and explicitly allowing only `unsafe-inline` where absolutely necessary (for the self-contained share page), we significantly reduce the risk of Cross-Site Scripting (XSS) attacks.

#### 2. Input Sanitization
Enhanced validation of User IDs and channel names to ensure identifiers conform to expected patterns.

#### 3. Data Minimization
Implemented the `DeleteUser` functionality to allow for complete purging of user data upon logout or account deletion, adhering to data privacy best practices.

#### 4. Rate Limiting
The `RateLimiter` now includes periodic cleanup of stale entries to prevent memory exhaustion attacks.

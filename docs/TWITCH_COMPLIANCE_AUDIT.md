# Twitch Terms of Service / Developer Agreement — Compliance Audit

**Project:** Twitch Room (Go backend + Chrome/Firefox browser extension)
**Audited scope:** `extension/` (manifest, background, content, popup, options) and supporting backend (`internal/auth`, `internal/identity`, CORS, share pages)
**Reference documents:**
- Twitch Terms of Service (last modified 08/12/2026) — https://legal.twitch.com/en/legal/terms-of-service/
- Twitch Developer Services Agreement (last modified 12/04/2024) — https://legal.twitch.com/en/legal/developer-agreement/
- Twitch Trademark Guidelines — https://legal.twitch.com/legal/trademark/
- Twitch Privacy Notice — https://legal.twitch.com/legal/privacy-notice/

> **Disclaimer:** This is an engineering/policy review performed by an AI agent, not legal advice. Twitch may enforce its terms at its sole discretion. Consult qualified counsel before shipping.

---

## 1. Verdict

**The extension, as currently implemented, does NOT meet Twitch's Terms of Service and Developer Agreement.** It contains several high-risk violations, primarily around (1) automated scraping and modification of the Twitch website, and (2) collecting and sharing Twitch user data with a third-party backend without documented consent/permission. Shipping in the current form risks account suspension, API key revocation, or legal action.

Severity tally (see §4):

| Severity | Count |
|---|---|
| 🔴 High | 6 |
| 🟠 Medium | 7 |
| 🟡 Low | 4 |

---

## 2. What was reviewed

- `extension/manifest.json` — permissions, host permissions, content scripts.
- `extension/content.js` — DOM scraping, SPA navigation tracking, injected badge UI.
- `extension/background.js` — OAuth (implicit grant), token storage, backend sync.
- `extension/popup/popup.js`, `extension/options.js` — UI, settings, auth.
- `internal/auth/auth.go`, `internal/identity/provider.go` — backend identity handling.
- `README.md` — documented architecture, CORS, auth modes.

---

## 3. Policy clauses most relevant to this extension

From the **Terms of Service §7 (License)** and **§10 (Prohibited Conduct)**:

- §7(d): no "use of any data mining, robots, or similar data gathering or extraction methods".
- §7(e): no downloading (except page caching) of any portion of the Twitch Services or information.
- §7(c)/(viii): no modifying or creating derivative works of the Twitch Services.
- §10(v): no collecting personal information about users or third parties without their consent.
- §10(xi): no accessing Twitch resources "by means of any robot, spider, scraper, crawler, or other automated means for any purpose".
- §10(xiii): no interfering with or disrupting the Twitch Services or other users' enjoyment.
- §10(xvi): no implying a relationship with or endorsement by Twitch without prior written permission.

From the **Developer Agreement**:

- II.B.3: do not "interfere with, modify, disrupt or disable features or functionality of the Program Materials" or access/use Twitch's or end users' properties in an unauthorized manner.
- VI.B–F (Data Policy): Twitch Data may only be used for limited purposes; may not be sold/licensed/monetized/shared with third parties without Twitch's prior written permission; must be deleted on request/un-auth; requires privacy notices and consent.
- VII: do not commingle or modify Program Materials to reveal incomplete data sets; keep a clear path to source.
- XI.I: may only access documented Program Materials.
- XIII.B (Privacy Obligations): legally adequate privacy notices, necessary consent, limited use, deletion on request.
- IV.C (Rate Limits): do not exceed or circumvent.
- IV.D (Security Measures): industry-standard security for user data and Program Materials.

---

## 4. Findings

### 🔴 F1 — Automated scraping of the Twitch website (ToS §7(d), §10(xi); Dev Agreement II.B.3, XI.I)

**Evidence:** `extension/content.js`
- `currentChannelFromUrl()` reads `window.location.pathname` to derive the channel being watched (lines 15–22).
- `scrapeTwitchInfo()` queries `meta[name="twitch-user-id"]` to extract the logged-in user's Twitch ID (lines 24–33).
- `MutationObserver` on `document.body` (subtree) plus `setInterval` every 1s re-run detection (lines 61–80).

**Impact:** This is exactly the "robot, spider, scraper, crawler, or other automated means" access Twitch prohibits, and it reads an undocumented meta tag (Dev Agreement XI.I). High risk of enforcement.

**Remediation:** Remove DOM scraping. Detect the watched channel via Twitch's documented APIs (e.g., the streamer page's own data is not an API; use Helix `streams`/`users` with the viewer's consent) or make channel entry manual. At minimum, stop reading the undocumented `twitch-user-id` meta tag and resolve identity solely through the documented OAuth `users` endpoint (already done in `loginWithTwitch`).

### 🔴 F2 — Injecting UI into Twitch pages (ToS §7(c), §7(viii), §10(xiii); Dev Agreement II.B.3)

**Evidence:** `extension/content.js` `injectUI()` (lines 82–129) appends a `#twitch-room-host` floating badge into `document.body` and re-injects it every 2s if removed.

**Impact:** Modifies the Twitch Services' DOM and can interfere with Twitch's own UI/monitoring. Explicitly prohibited as modifying/derivative use.

**Remediation:** Remove injected on-page UI. Move all user-facing surfaces to the extension popup/options (already present) or a Twitch Extension (submitted via Twitch's Extension program) if on-page presence is required.

### 🔴 F3 — Collecting personal information without consent (ToS §10(v); Dev Agreement XIII.B)

**Evidence:** `extension/content.js` extracts the logged-in user's ID from a meta tag and forwards it to `background.js` (`WATCHING_CHANNEL`), which stores it and syncs to the backend. No privacy notice or consent gate exists anywhere in the extension or backend.

**Impact:** Collecting a user identifier without a legally adequate privacy notice and consent violates the Developer Agreement's privacy obligations and ToS §10(v).

**Remediation:** Add a privacy notice + explicit opt-in consent screen before any collection; document data practices; provide an in-extension link to the privacy policy.

### 🔴 F4 — Sharing Twitch user data with a third-party backend without permission (Dev Agreement VI.B–VI.C, VI.F)

**Evidence:** `extension/background.js` `syncWithBackend()` (lines 227–256) sends `stream_ids`, `Authorization: Bearer <Twitch token>`, and `X-User-ID: <login>` to `API_BASE` (a separate service). The backend persists watch states and user IDs. `fetchFeed()` shares the same data.

**Impact:** Twitch Data (user IDs, watch activity) is made available to a third party (the Twitch Room backend) without Twitch's prior written permission, and is used beyond the permitted purposes. Also conflicts with VI.F (deletion on un-auth) — logout only clears local storage, not server-side state.

**Remediation:** Either (a) obtain Twitch's prior written permission for this data use, or (b) re-architect so the backend never receives raw Twitch tokens/IDs as the source of truth (e.g., users self-identify with a non-Twitch handle, and Twitch data is only used transiently with consent). Implement server-side deletion on logout/un-auth (call `DELETE /v1/watch` and purge subscriptions).

### 🔴 F5 — Using an undocumented data source (Dev Agreement XI.I)

**Evidence:** `extension/content.js` line 29: `document.querySelector('meta[name="twitch-user-id"]')`.

**Impact:** Accessing data not documented on the Twitch Developer Site is prohibited.

**Remediation:** Remove this code path; resolve identity only through the documented Helix `users` endpoint after OAuth (already implemented in `loginWithTwitch`).

### 🔴 F6 — Implied affiliation / endorsement (ToS §10(xvi))

**Evidence:** Product name "Twitch Room", injected badge label "Twitch Room", host permissions and content scripts on `*.twitch.tv`. No disclaimer that the extension is not affiliated with or endorsed by Twitch.

**Impact:** Using the Twitch name in the product name without prior written permission risks trademark and endorsement issues (ToS §10(xvi); Trademark Guidelines).

**Remediation:** Rename to something not containing "Twitch" (or obtain written permission), and add an explicit "not affiliated with Twitch" disclaimer in the extension listing, popup, and options page.

### 🟠 F7 — Token storage and security (Dev Agreement IV.D; recent Twitch security advisories)

**Evidence:** `extension/background.js` stores `AUTH_TOKEN`, `TWITCH_ACCESS_TOKEN`, and `USER_ID` in `chrome.storage.local` (lines 164–168, 88). Backend trusts client-supplied `X-User-ID` whenever `ALLOW_DEV_AUTH=true` (auth.go lines 56–58).

**Impact:** Tokens at rest in extension storage are a known credential-exposure risk (see recent malicious-extension advisories). Dev auth in production would let any caller impersonate any user.

**Remediation:** Keep tokens in memory where possible; use `chrome.identity` ephemeral tokens; encrypt at rest if persisted. Enforce `ALLOW_DEV_AUTH=false` and `APP_ENV=production` in production (already refused by backend) and validate the Twitch token server-side rather than trusting `X-User-ID`.

### 🟠 F8 — Over-broad OAuth scope (Dev Agreement VI.B)

**Evidence:** `extension/background.js` line 120: `scope = 'user:read:follows'`, but the codebase never uses follows data.

**Impact:** Requesting permissions not needed violates least-privilege and Twitch's data-minimization expectations.

**Remediation:** Request only scopes actually used. If none are needed beyond identity, omit scope or use the minimal profile scope.

### 🟠 F9 — Implicit grant flow (legacy) (Dev Agreement VI.E)

**Evidence:** `extension/background.js` lines 119: `response_type=token`.

**Impact:** Implicit grant is legacy; Twitch recommends authorization code flow. Tokens are exposed in redirect URL fragments and cannot be refreshed.

**Remediation:** Migrate to authorization code flow with PKCE, or rely on the backend's existing `/auth/twitch` code-exchange flow (already implemented server-side) instead of `chrome.identity`.

### 🟠 F10 — Excessive polling / performance impact (ToS §10(xiii))

**Evidence:** `extension/content.js`: `MutationObserver` on full subtree (line 70), `setInterval` 1s (line 75) and 2s (line 124).

**Impact:** Continuous DOM observation can degrade page performance and be viewed as interfering with other users' enjoyment.

**Remediation:** Remove content script entirely (per F1/F2). If retained, throttle and scope observers to specific elements.

### 🟠 F11 — No privacy notice or consent (Dev Agreement XIII.B; ToS §10(v))

**Evidence:** No privacy policy file, no consent UI in `popup/` or `options.html`, no disclosure in README.

**Impact:** Fails privacy obligations; blocks compliant data collection.

**Remediation:** Publish a privacy policy; add consent + disclosure in the extension and backend; honor deletion requests.

### 🟠 F12 — No data-deletion path on un-auth (Dev Agreement VI.F)

**Evidence:** `LOGOUT_TWITCH` (background.js lines 87–92) only removes local keys; no call to `DELETE /v1/watch` or account purge.

**Impact:** Retains Twitch Data after authorization is revoked, violating VI.F.

**Remediation:** On logout, call `DELETE /v1/watch` and request backend purge of the user's record; document retention.

### 🟠 F13 — Production endpoint / mixed content (Dev Agreement IV.D; ToS §10(xvii))

**Evidence:** `manifest.json` line 14: `"http://localhost:8080/*"`; `background.js` line 6: `API_BASE = 'http://localhost:8080'`; popup copy-link hardcodes `http://localhost:8080`.

**Impact:** Shipping with HTTP localhost endpoints breaks production use and mixed-content rules; not a direct ToS violation but a compliance/deployment blocker.

**Remediation:** Use HTTPS production origin; remove localhost from production manifest; make API base configurable (already partially via options).

### 🟡 F14 — Rate-limit / abusive usage risk (Dev Agreement IV.C)

**Evidence:** Content script polls every 1s and re-injects every 2s; backend has rate limits but client could be seen as excessive.

**Impact:** Low today (no Twitch API calls from content script), but pattern is risky.

**Remediation:** Remove polling (per F1/F2).

### 🟡 F15 — Branding without permission (ToS §10(xvi); Trademark Guidelines)

**Evidence:** Name "Twitch Room" used in manifest `name`, injected badge, popup. No "not affiliated" disclaimer.

**Impact:** Potential trademark issue.

**Remediation:** Rename or obtain written permission; add disclaimer.

### 🟡 F16 — Undeclared data collection in manifest (Chrome policy; Twitch data minimization)

**Evidence:** Manifest requests `identity`, `storage`, `alarms`, `notifications` plus host permissions on all of `*.twitch.tv`. Notifications/alarms usage is not visible in the reviewed code (alarms not used).

**Impact:** Unused permissions reduce store approval odds and conflict with data minimization.

**Remediation:** Remove unused permissions (`alarms`, `notifications`) unless implemented; justify host permissions.

### 🟡 F17 — Public unauthenticated share pages exposing watch state (ToS §10(v); Dev Agreement VI.B)

**Evidence:** README: `/u/{user_id}` unauthenticated, shows watch state when visibility is Public.

**Impact:** Publishing a Twitch user's watch state publicly may conflict with privacy expectations unless clearly opt-in and consented. Already gated by "Public" visibility and 404 otherwise; acceptable if consented.

**Remediation:** Ensure the "Public" toggle is accompanied by an explicit consent + privacy notice; add a takedown/deletion path.

---

## 5. What already aligns well

- Uses Twitch OAuth (documented) for identity rather than scraping passwords.
- Backend validates identifiers and caps payloads; enforces HTTPS/HSTS/CSP/rate limits (README).
- CORS is restricted to configured origins; dev defaults are documented.
- `ALLOW_DEV_AUTH` refuses production (auth.go + README).
- Watch visibility/subscription model is opt-in and server-enforced.
- Enrichment uses documented Helix endpoints with app credentials.
- Share pages return 404 for non-public states (no existence leak).
- No Bits/commerce/NFT features that would trigger those policy sections.

---

## 6. Prioritized remediation plan

**P0 (block release):** F1, F2, F4, F5, F6, F11 — remove scraping/DOM injection, stop sharing Twitch Data without permission, add privacy notice/consent, rename/rebrand.
**P1 (before production):** F3, F7, F9, F12, F13, F17.
**P2 (polish):** F8, F10, F14, F15, F16.

A compliant v2 would keep the extension as a pure popup/options client that: authenticates via the backend's documented OAuth code flow (or minimal-scope `chrome.identity`), never injects into or scrapes Twitch pages, never forwards raw Twitch tokens to third parties beyond the documented flow, and ships with a privacy policy + consent + deletion path.

---

## 7. Residual risk

Even after remediation, Twitch's Terms of Service are enforced at Twitch's sole discretion (ToS §18, Dev Agreement IX). A browser extension that reads the user's currently-watched channel is inherently adjacent to prohibited "automated access"; consider seeking Twitch's written guidance or restructuring around user-initiated, manual input. This audit is not legal advice.

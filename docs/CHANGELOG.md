# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased] - 2026-10-21

### Changed
- **Auth:** OAuth state is now signed and bound to the browser, so logins work across instances/restarts.
- **API:** `POST /v1/watch` returns 200 on update, 201 on first creation; added `DELETE /v1/account`.
- **Logout:** no longer purges account data; explicit delete endpoint added.
- **Refresh:** Helix lookups chunked at 100; refresh pages states in batches.
- **Extension:** feed rendered without `innerHTML`; clearer expired-session errors.
- **Ops:** Docker image runs as non-root with healthcheck; toolchain aligned to Go 1.26.

## [Unreleased] - 2026-10-19

### Added
- **Data Privacy**: Added functionality to purge all user-related data (watch states, settings, and subscriptions).
- **UI/UX**: Significant improvements to the extension popup (toast messages, status updates, recent channels).
- **UI/UX**: Responsive design and theme toggling for the public share page.
- **Stability**: Periodic background cleanup for all caches, session stores, and rate limiters.

### Changed
- **Refactor**: Integrated `context.Context` throughout the service layer for improved cancellation/timeout handling.
- **Security**: Strengthened Content Security Policy (CSP) for the public share page.
- **Security**: Improved input sanitization and logging for database operations.
- **UX**: Updated the public share page refresh interval and added a manual refresh button.

### Removed
- Deleted `extension/content.js`.
- Cleaned up unused code in `main.go` and `internal/security/middleware_test.go`.

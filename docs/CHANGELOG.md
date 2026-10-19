# Changelog

All notable changes to this project will be documented in this file.

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

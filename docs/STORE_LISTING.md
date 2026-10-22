# Chrome Web Store / Firefox Add-ons Listing — Watch Room

## Name
Watch Room

## Short description
Share what you are watching on Twitch with friends. Not affiliated with or endorsed by Twitch.

## Detailed description
Watch Room lets you publish the Twitch streams you are currently watching and see what your friends are watching in a feed. You choose who can subscribe to you (open or approved) and who can see your watch state (public, subscribers only, or private). Every publisher also gets an optional read-only public page.

Watch state is entered manually by you — Watch Room never scrapes or modifies twitch.tv pages. Authentication uses Twitch OAuth. Data is purged on request via "Delete my data".

## Permissions justification
- `storage` — store auth token, user id, settings, consent flag, and cached watch state locally.
- `identity` — run the Twitch OAuth flow via `chrome.identity`.
- `activeTab` — read the current tab's URL when you click "use current tab" to import a channel name.
- Host permissions (runtime, optional) — talk to the Watch Room API you configure (your own backend).

## Privacy
- Privacy policy: <HOSTED_URL>/privacy.html (publish `extension/privacy.html` at a public URL)
- Data collected: Twitch login, published watch state, subscription relationships, publisher settings.
- Data is not sold. Deleted on request via "Delete my data".

## Icons
- 16/32/48/128 provided in `extension/icons/`.

## Notes for review
- No content scripts; no DOM modification of twitch.tv.
- No scraping of Twitch pages; channel entry is manual.
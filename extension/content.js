/**
 * Twitch Room Content Script
 * Scrapes channel name and user ID from the Twitch DOM.
 */

function scrapeTwitchInfo() {
  let channelName = null;
  let userId = null;

  // 1. Try to get channel name from URL (most reliable in SPA)
  const path = window.location.pathname.replace('/', '');
  if (path && !['directory', 'videos', 'settings', 'i'].includes(path)) {
    channelName = path;
  }

  // 2. Try to scrape channel name from DOM (e.g., the header)
  if (!channelName) {
    const titleElement = document.querySelector('h1');
    if (titleElement) {
      channelName = titleElement.textContent.trim();
    }
  }

  // 3. Try to find User ID in the DOM
  // Looking for common patterns like data-user-id or similar
  const elements = document.querySelectorAll('*');
  for (let i = 0; i < elements.length; i++) {
    const el = elements[i];
    if (el.getAttribute('data-user-id')) {
      userId = el.getAttribute('data-user-id');
      break;
    }
    // Sometimes it's in a script tag or meta tag, but we can't easily read script content
    // without injecting. We'll check meta tags for any hint.
  }

  // Fallback: if we couldn't find userId, we might try to find it in a meta tag
  if (!userId) {
    const meta = document.querySelector('meta[name="twitch-user-id"]');
    if (meta) {
      userId = meta.getAttribute('content');
    }
  }

  return { channelName, userId };
}

function reportStatus() {
  const info = scrapeTwitchInfo();
  if (info.channelName) {
    console.log('[Twitch Room] Reporting status:', info);
    chrome.runtime.sendMessage({
      type: 'WATCHING_CHANNEL',
      payload: info
    }, (response) => {
      if (chrome.runtime.lastError) {
        console.error('[Twitch Room] Error sending message:', chrome.runtime.lastError.message);
      } else {
        console.log('[Twitch Room] Response:', response);
      }
    });
  }
}

// Watch for URL changes and DOM changes (Twitch is a Single Page App)
let lastUrl = window.location.href;
const observer = new MutationObserver(() => {
  if (window.location.href !== lastUrl) {
    lastUrl = window.location.href;
    console.log('[Twitch Room] URL changed, re-scraping...');
    reportStatus();
  }
});

// Start observing
observer.observe(document.body, { childList: true, subtree: true });

// Initial run
console.log('[Twitch Room] Content script initialized');
reportStatus();

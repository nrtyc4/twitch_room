/**
 * Twitch Room Content Script
 * Detects the channel a user is currently watching on Twitch and reports it
 * to the background script.
 */

const NON_CHANNEL_SEGMENTS = new Set([
  'directory', 'videos', 'settings', 'i', 'search', 'following', 'friends',
  'subscriptions', 'wallet', 'drops', 'inventory', 'broadcast', 'popout',
]);

let lastReportedChannel = null;
let reportTimer = null;

function currentChannelFromUrl() {
  const path = window.location.pathname.replace(/^\/+/, '').split('/')[0];
  if (!path) return null;
  if (NON_CHANNEL_SEGMENTS.has(path.toLowerCase())) return null;
  // Twitch channel logins are 4-25 chars of [a-z0-9_].
  if (!/^[a-zA-Z0-9_]{3,25}$/.test(path)) return null;
  return path;
}

function scrapeTwitchInfo() {
  const channelName = currentChannelFromUrl();
  let userId = null;

  // Best-effort: Twitch stores the logged-in user id in a meta tag on some pages.
  const meta = document.querySelector('meta[name="twitch-user-id"]');
  if (meta) userId = meta.getAttribute('content');

  return { channelName, userId };
}

function reportStatus() {
  const info = scrapeTwitchInfo();
  if (!info.channelName) {
    // Only clear state if we previously reported a channel.
    if (lastReportedChannel) {
      lastReportedChannel = null;
      chrome.runtime.sendMessage({ type: 'WATCHING_CHANNEL', payload: { channelName: null, userId: info.userId } });
    }
    return;
  }
  if (info.channelName === lastReportedChannel) return;
  lastReportedChannel = info.channelName;
  console.log('[Twitch Room] Reporting status:', info);
  chrome.runtime.sendMessage({ type: 'WATCHING_CHANNEL', payload: info }, (response) => {
    if (chrome.runtime.lastError) {
      console.error('[Twitch Room] Error sending message:', chrome.runtime.lastError.message);
    }
  });
}

function scheduleReport() {
  if (reportTimer) clearTimeout(reportTimer);
  reportTimer = setTimeout(reportStatus, 300);
}

// Watch for SPA navigation and DOM mutations.
let lastUrl = window.location.href;
const observer = new MutationObserver(() => {
  if (window.location.href !== lastUrl) {
    lastUrl = window.location.href;
    scheduleReport();
  }
});

if (document.body) {
  observer.observe(document.body, { childList: true, subtree: true });
}

// Initial run + periodic safety net (Twitch sometimes updates without URL change).
scheduleReport();
setInterval(() => {
  if (window.location.href !== lastUrl) {
    lastUrl = window.location.href;
    scheduleReport();
  }
}, 1000);

// --- Injected UI (floating badge) ---
let injectedRoot = null;

function injectUI() {
  if (injectedRoot && document.body.contains(injectedRoot)) return;
  const host = document.createElement('div');
  host.id = 'twitch-room-host';
  host.style.cssText = 'position:fixed;bottom:16px;right:16px;z-index:2147483647;';
  const shadow = host.attachShadow({ mode: 'open' });
  shadow.innerHTML = `
    <style>
      .badge { background:#9146FF;color:#fff;padding:8px 12px;border-radius:8px;font-family:system-ui,sans-serif;font-size:13px;box-shadow:0 2px 8px rgba(0,0,0,.3); }
      .badge small { display:block;opacity:.8;font-size:11px; }
    </style>
    <div class="badge">Twitch Room<div id="sub" small></div></div>
  `;
  document.body.appendChild(host);
  injectedRoot = host;

  // Show friend activity count.
  chrome.runtime.sendMessage({ type: 'GET_FEED' }, (resp) => {
    const sub = shadow.getElementById('sub');
    if (!sub) return;
    if (resp && resp.status === 'success' && Array.isArray(resp.data)) {
      sub.textContent = `${resp.data.length} friends active`;
    } else {
      sub.textContent = 'Not connected';
    }
  });
}

// Inject once DOM is ready and on navigation.
function tryInject() {
  if (document.body) injectUI();
}
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', tryInject);
} else {
  tryInject();
}

// Re-inject on SPA navigation if removed.
setInterval(() => {
  if (!document.getElementById('twitch-room-host') && document.body) {
    tryInject();
  }
}, 2000);

console.log('[Twitch Room] Content script initialized');
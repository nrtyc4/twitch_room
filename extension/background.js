/**
 * Twitch Room Background Script
 * Handles API communication and manages the current watching state.
 */

let API_BASE = 'http://localhost:8080';
chrome.storage.local.get(['API_BASE'], (r) => {
  if (r && r.API_BASE) API_BASE = r.API_BASE;
});

// Twitch application client id. Replace with your own from the Twitch Dev Console,
// or set it via the extension options page (stored as TWITCH_CLIENT_ID).
const TWITCH_CLIENT_ID = '';

// State in memory for fast access
let currentWatchState = {
  channelName: null,
  userId: null,
  lastUpdated: null
};

// NOTE: No content script is used anymore. Watch state is entered manually by the
// user (see popup) to comply with Twitch's Terms of Service (no automated
// scraping or DOM modification of twitch.tv).

// Listen for messages from popup
chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  if (message.type === 'SYNC_NOW') {
    const channelName = (message.payload && message.payload.channelName) || currentWatchState.channelName;
    syncWithBackend(channelName, currentWatchState.userId)
      .then(() => sendResponse({ status: 'success' }))
      .catch(err => sendResponse({ status: 'error', error: err.message }));
    return true;
  }
  
  if (message.type === 'GET_CURRENT_STATUS') {
    chrome.storage.local.get(['currentWatchState'], (result) => {
      sendResponse({ 
        status: 'ok', 
        state: result.currentWatchState || { channelName: null, userId: null } 
      });
    });
    return true;
  }

  if (message.type === 'GET_FEED') {
    fetchFeed().then(feed => sendResponse({ status: 'success', data: feed }))
      .catch(err => sendResponse({ status: 'error', error: err.message }));
    return true;
  }

  if (message.type === 'GET_USER_INFO') {
    getUserInfo().then(info => sendResponse({ status: 'success', data: info }))
      .catch(err => sendResponse({ status: 'error', error: err.message }));
    return true;
  }

  if (message.type === 'LOGIN_TWITCH') {
    loginWithTwitch()
      .then(result => sendResponse({ status: 'success', data: result }))
      .catch(err => sendResponse({ status: 'error', error: err.message }));
    return true;
  }

  if (message.type === 'LOGOUT_TWITCH') {
    (async () => {
      try {
        const s = await chrome.storage.local.get(['API_BASE', 'AUTH_TOKEN', 'USER_ID']);
        const apiBase = (s.API_BASE || API_BASE || 'http://localhost:8080').replace(/\/$/, '');
        if (s.AUTH_TOKEN || s.USER_ID) {
          await fetch(`${apiBase}/auth/logout`, {
            method: 'POST',
            headers: {
              'Authorization': `Bearer ${s.AUTH_TOKEN || ''}`,
              'X-User-ID': s.USER_ID || '',
            },
            credentials: 'include',
          }).catch(() => {});
        }
      } catch (e) { /* ignore */ }
      chrome.storage.local.remove(['AUTH_TOKEN', 'USER_ID', 'TWITCH_ACCESS_TOKEN', 'PRIVACY_CONSENT'], () => {
        sendResponse({ status: 'success' });
      });
    })();
    return true;
  }
});

/**
 * Returns the Twitch client id to use, preferring a value stored in
 * extension storage over the bundled constant.
 */
async function getClientId() {
  const stored = await chrome.storage.local.get(['TWITCH_CLIENT_ID']);
  if (stored.TWITCH_CLIENT_ID) return stored.TWITCH_CLIENT_ID;
  if (TWITCH_CLIENT_ID) return TWITCH_CLIENT_ID;
  // Fall back to the backend's configured (public) client id so users never
  // have to enter one themselves.
  try {
    const s = await chrome.storage.local.get(['API_BASE']);
    const apiBase = (s.API_BASE || API_BASE || 'http://localhost:8080').replace(/\/$/, '');
    const res = await fetch(`${apiBase}/auth/twitch/client-id`);
    if (res.ok) {
      const data = await res.json();
      if (data && data.client_id) {
        await chrome.storage.local.set({ TWITCH_CLIENT_ID: data.client_id });
        return data.client_id;
      }
    }
  } catch (e) { /* ignore */ }
  return '';
}

/**
 * Runs the Twitch OAuth (implicit grant) flow via chrome.identity and stores
 * the resulting access token + user id in extension storage.
 */
async function loginWithTwitch() {
  const clientId = await getClientId();
  if (!clientId) {
    throw new Error('Missing Twitch client id. Set it in the extension settings.');
  }

  const redirectUri = chrome.identity.getRedirectURL();
  const authUrl = new URL('https://id.twitch.tv/oauth2/authorize');
  authUrl.searchParams.set('client_id', clientId);
  authUrl.searchParams.set('redirect_uri', redirectUri);
  authUrl.searchParams.set('response_type', 'token');
  // Minimal scopes: identity only. No follows/data scopes are used.
  authUrl.searchParams.set('force_verify', 'true');

  const responseUrl = await new Promise((resolve, reject) => {
    chrome.identity.launchWebAuthFlow(
      { url: authUrl.toString(), interactive: true },
      (redirectedTo) => {
        if (chrome.runtime.lastError) {
          reject(new Error(chrome.runtime.lastError.message));
          return;
        }
        if (!redirectedTo) {
          reject(new Error('OAuth flow was cancelled.'));
          return;
        }
        resolve(redirectedTo);
      }
    );
  });

  // The access token comes back in the URL fragment.
  const hash = responseUrl.split('#')[1] || '';
  const params = new URLSearchParams(hash);
  const accessToken = params.get('access_token');
  if (!accessToken) {
    throw new Error('No access token returned by Twitch.');
  }

  // Resolve the Twitch user id (login) using the token.
  const userResp = await fetch('https://api.twitch.tv/helix/users', {
    headers: {
      'Authorization': `Bearer ${accessToken}`,
      'Client-ID': clientId,
    },
  });
  if (!userResp.ok) {
    throw new Error(`Failed to resolve Twitch user: ${userResp.status}`);
  }
  const userData = await userResp.json();
  const login = userData?.data?.[0]?.login;
  if (!login) {
    throw new Error('Could not resolve Twitch user login.');
  }

  await chrome.storage.local.set({
    AUTH_TOKEN: accessToken,
    USER_ID: login,
    TWITCH_ACCESS_TOKEN: accessToken,
  });

  return { user_id: login, access_token: accessToken };
}

/**
 * Fetches the social feed from the backend.
 */
async function fetchFeed() {
  const settings = await chrome.storage.local.get(['AUTH_TOKEN', 'USER_ID']);
  
  if (!settings.AUTH_TOKEN || !settings.USER_ID) {
    throw new Error('Missing AUTH_TOKEN or USER_ID. Please configure them in settings or log in with Twitch.');
  }

  const response = await fetch(`${API_BASE}/v1/feed`, {
    method: 'GET',
    headers: {
      'Authorization': `Bearer ${settings.AUTH_TOKEN}`,
      'X-User-ID': settings.USER_ID
    }
  });

  if (response.status === 401) {
    throw new Error('Session expired. Please log in again.');
  }
  if (!response.ok) {
    throw new Error(`HTTP error! status: ${response.status}`);
  }

  return await response.json();
}

/**
 * Fetches user info from the backend.
 */
async function getUserInfo() {
  const settings = await chrome.storage.local.get(['AUTH_TOKEN', 'USER_ID']);
  
  if (!settings.AUTH_TOKEN || !settings.USER_ID) {
    throw new Error('Missing AUTH_TOKEN or USER_ID. Please configure them in settings or log in with Twitch.');
  }

  const response = await fetch(`${API_BASE}/auth/me`, {
    method: 'GET',
    headers: {
      'Authorization': `Bearer ${settings.AUTH_TOKEN}`,
      'X-User-ID': settings.USER_ID
    }
  });

  if (response.status === 401) {
    throw new Error('Session expired. Please log in again.');
  }
  if (!response.ok) {
    throw new Error(`HTTP error! status: ${response.status}`);
  }

  return await response.json();
}

/**
 * Performs the API call to the backend.
 * Includes Authorization and X-User-ID headers from storage.
 */
async function syncWithBackend(channelName, userId) {
  if (!channelName) {
    console.log('[Watch Room] No active channel to sync. Skipping.');
    return { status: 'skipped' };
  }

  const consent = await chrome.storage.local.get(['PRIVACY_CONSENT']);
  if (!consent.PRIVACY_CONSENT) {
    throw new Error('Privacy consent is required before syncing. Please accept the privacy policy in settings.');
  }

  const settings = await chrome.storage.local.get(['AUTH_TOKEN', 'USER_ID']);
  
  if (!settings.AUTH_TOKEN || !settings.USER_ID) {
    const errorMsg = 'Missing AUTH_TOKEN or USER_ID. Please configure them in settings or log in with Twitch.';
    console.warn(`[Watch Room] ${errorMsg}`);
    throw new Error(errorMsg);
  }

  const response = await fetch(`${API_BASE}/v1/watch`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${settings.AUTH_TOKEN}`,
      'X-User-ID': settings.USER_ID
    },
    body: JSON.stringify({ stream_ids: [channelName] })
  });

  if (response.status === 401) {
    throw new Error('Session expired. Please log in again.');
  }
  if (!response.ok) {
    throw new Error(`HTTP error! status: ${response.status}`);
  }

  return await response.json();
}
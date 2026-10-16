/**
 * Twitch Room Background Script
 * Handles API communication and manages the current watching state.
 */

const API_BASE = 'http://localhost:8080';

// State in memory for fast access
let currentWatchState = {
  channelName: null,
  userId: null,
  lastUpdated: null
};

// Listen for messages from content.js
chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  if (message.type === 'WATCHING_CHANNEL') {
    const { channelName, userId } = message.payload;
    console.log('[Twitch Room] Received watch state from content script:', { channelName, userId });
    
    currentWatchState = {
      channelName,
      userId,
      lastUpdated: Date.now()
    };

    // Update the stored "current state"
    chrome.storage.local.set({ currentWatchState }, () => {
      console.log('[Twitch Room] Current state updated in storage');
    });

    // Trigger sync with backend
    syncWithBackend(channelName, userId).catch(err => {
      console.error('[Twitch Room] Initial sync failed:', err.message);
    });

    sendResponse({ status: 'received' });
  }
  return true; // Keep channel open for async response
});

// Listen for messages from popup
chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  if (message.type === 'SYNC_NOW') {
    syncWithBackend(currentWatchState.channelName, currentWatchState.userId)
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
});

/**
 * Fetches the social feed from the backend.
 */
async function fetchFeed() {
  const settings = await chrome.storage.local.get(['AUTH_TOKEN', 'USER_ID']);
  
  if (!settings.AUTH_TOKEN || !settings.USER_ID) {
    throw new Error('Missing AUTH_TOKEN or USER_ID. Please configure them in settings.');
  }

  const response = await fetch(`${API_BASE}/v1/feed`, {
    method: 'GET',
    headers: {
      'Authorization': `Bearer ${settings.AUTH_TOKEN}`,
      'X-User-ID': settings.USER_ID
    }
  });

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
    throw new Error('Missing AUTH_TOKEN or USER_ID. Please configure them in settings.');
  }

  const response = await fetch(`${API_BASE}/auth/me`, {
    method: 'GET',
    headers: {
      'Authorization': `Bearer ${settings.AUTH_TOKEN}`,
      'X-User-ID': settings.USER_ID
    }
  });

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
    console.log('[Twitch Room] No active channel to sync. Skipping.');
    return { status: 'skipped' };
  }

  const settings = await chrome.storage.local.get(['AUTH_TOKEN', 'USER_ID']);
  
  if (!settings.AUTH_TOKEN || !settings.USER_ID) {
    const errorMsg = 'Missing AUTH_TOKEN or USER_ID. Please configure them in settings.';
    console.warn(`[Twitch Room] ${errorMsg}`);
    throw new Error(errorMsg);
  }

  console.log(`[Twitch Room] Syncing with backend: ${channelName} (User: ${settings.USER_ID})`);

  try {
    const response = await fetch(`${API_BASE}/v1/watch`, {
      method: 'POST',
      headers: {
        'Authorization': `Bearer ${settings.AUTH_TOKEN}`,
        'X-User-ID': settings.USER_ID,
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({
        stream_ids: [channelName]
      })
    });

    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`);
    }

    console.log('[Twitch Room] Sync successful');
    return await response.json();
  } catch (error) {
    console.error('[Twitch Room] Sync failed:', error);
    throw error;
  }
}

// Periodic syncing (every 5 minutes)
chrome.alarms.create('syncAlarm', { periodInMinutes: 5 });
chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === 'syncAlarm') {
    console.log('[Twitch Room] Running periodic sync...');
    syncWithBackend(currentWatchState.channelName, currentWatchState.userId).catch(err => {
      console.error('[Twitch Room] Periodic sync failed:', err.message);
    });
  }
});

console.log('[Twitch Room] Background script initialized');

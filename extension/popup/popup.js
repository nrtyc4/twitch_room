/**
 * Twitch Room Popup Script
 * Manages the popup UI and interactions.
 */
document.addEventListener('DOMContentLoaded', async () => {
  const watchingText = document.getElementById('watching-text');
  const lastSyncedEl = document.getElementById('last-synced');
  const syncBtn = document.getElementById('sync-btn');
  const saveSettingsBtn = document.getElementById('save-settings-btn');
  const openOptionsLink = document.getElementById('open-options');
  const loginTwitchBtn = document.getElementById('login-twitch-btn');
  const logoutTwitchBtn = document.getElementById('logout-twitch-btn');
  const authStatus = document.getElementById('auth-status');
  const clientIdInput = document.getElementById('client-id');
  const authTokenInput = document.getElementById('auth-token');
  const userIdInput = document.getElementById('user-id');
  const statusMessage = document.getElementById('status-message');
  const pulseContainer = document.getElementById('pulse-container');
  const userAvatar = document.getElementById('user-avatar');
  const userDisplay = document.getElementById('user-display');
  const feedContainer = document.getElementById('feed-container');
  const channelInput = document.getElementById('channel-input');
  const recentDatalist = document.getElementById('recent-channels');
  const channelHint = document.getElementById('channel-hint');
  const toastEl = document.getElementById('toast');
  const useTabBtn = document.getElementById('use-tab-btn');

  const NON_CHANNEL_SEGMENTS = new Set(['directory', 'videos', 'settings', 'i', 'search', 'following', 'friends', 'subscriptions', 'wallet', 'drops', 'inventory', 'broadcast', 'popout']);

  function showToast(text, type = 'info') {
    if (!toastEl) return;
    toastEl.textContent = text;
    toastEl.className = 'toast toast-' + type + ' toast-show';
    clearTimeout(toastEl._t);
    toastEl._t = setTimeout(() => { toastEl.classList.remove('toast-show'); }, 2500);
  }

  function showStatus(text, type = 'info') {
    if (!statusMessage) return;
    statusMessage.textContent = text;
    statusMessage.className = 'status-message';
    if (type === 'error') statusMessage.classList.add('error-text');
    if (type === 'success') statusMessage.classList.add('success-text');
    setTimeout(() => {
      if (statusMessage.textContent === text) {
        statusMessage.textContent = '';
        statusMessage.className = 'status-message';
      }
    }, 3000);
  }

  function channelFromUrl(url) {
    try {
      const u = new URL(url);
      if (!/twitch\.tv$/i.test(u.hostname) && !/\.twitch\.tv$/i.test(u.hostname)) return null;
      const seg = u.pathname.replace(/^\/+/, '').split('/')[0];
      if (!seg) return null;
      if (NON_CHANNEL_SEGMENTS.has(seg.toLowerCase())) return null;
      if (!/^[a-zA-Z0-9_]{3,25}$/.test(seg)) return null;
      return seg;
    } catch (e) {
      return null;
    }
  }

  function isValidChannel(value) {
    const v = (value || '').trim();
    if (!v) return false;
    if (/^https?:\/\//i.test(v)) return !!channelFromUrl(v);
    return /^[a-zA-Z0-9_]{3,25}$/.test(v);
  }

  function normalizeChannel(value) {
    const v = (value || '').trim();
    if (/^https?:\/\//i.test(v)) return channelFromUrl(v);
    return v;
  }

  function updateSyncButtonState() {
    if (!syncBtn || !channelInput) return;
    const ok = isValidChannel(channelInput.value);
    syncBtn.disabled = !ok;
    if (channelHint) {
      channelHint.textContent = ok ? 'Ready to sync.' : 'Enter a valid channel name or Twitch URL.';
      channelHint.className = 'hint ' + (ok ? 'hint-ok' : 'hint-warn');
    }
  }

  function updateLastSynced(ts) {
    if (!lastSyncedEl) return;
    if (!ts) { lastSyncedEl.textContent = 'Never synced'; return; }
    const d = new Date(ts);
    lastSyncedEl.textContent = 'Last synced ' + d.toLocaleTimeString();
  }

  async function loadRecentChannels() {
    const r = await chrome.storage.local.get(['RECENT_CHANNELS']);
    const list = Array.isArray(r.RECENT_CHANNELS) ? r.RECENT_CHANNELS : [];
    if (recentDatalist) {
      recentDatalist.innerHTML = '';
      list.forEach(c => {
        const opt = document.createElement('option');
        opt.value = c;
        recentDatalist.appendChild(opt);
      });
    }
  }

  async function pushRecentChannel(channel) {
    const r = await chrome.storage.local.get(['RECENT_CHANNELS']);
    const list = Array.isArray(r.RECENT_CHANNELS) ? r.RECENT_CHANNELS : [];
    const next = [channel].concat(list.filter(c => c !== channel)).slice(0, 7);
    await chrome.storage.local.set({ RECENT_CHANNELS: next });
    await loadRecentChannels();
  }

  // Load current settings from storage
  chrome.storage.local.get(['AUTH_TOKEN', 'USER_ID', 'TWITCH_CLIENT_ID', 'PRIVACY_CONSENT', 'API_BASE', 'LAST_SYNCED', 'RECENT_CHANNELS'], (result) => {
    if (result.TWITCH_CLIENT_ID) clientIdInput.value = result.TWITCH_CLIENT_ID;
    if (result.AUTH_TOKEN) authTokenInput.value = result.AUTH_TOKEN;
    if (result.USER_ID) userIdInput.value = result.USER_ID;
    const consentBox = document.getElementById('privacy-consent');
    if (consentBox && result.PRIVACY_CONSENT) consentBox.checked = true;
    updateAuthButtons(!!result.AUTH_TOKEN);
    loadUserProfile();
    loadFeed();
    updateLastSynced(result.LAST_SYNCED);
    loadRecentChannels();
    updateSyncButtonState();
  });

  async function loadUserProfile() {
    chrome.runtime.sendMessage({ type: 'GET_USER_INFO' }, (response) => {
      if (chrome.runtime.lastError) return;
      if (response && response.status === 'success' && response.data) {
        if (response.data.avatar_url) userAvatar.src = response.data.avatar_url;
        if (response.data.display_name) userDisplay.textContent = response.data.display_name;
      } else {
        userDisplay.textContent = 'Not signed in';
      }
    });
  }

  async function loadFeed() {
    chrome.runtime.sendMessage({ type: 'GET_FEED' }, (response) => {
      if (chrome.runtime.lastError) {
        const errP = document.createElement('p');
        errP.className = 'feed-empty-msg';
        errP.textContent = 'Error loading feed.';
        feedContainer.replaceChildren(errP);
        return;
      }
      if (response && response.status === 'success' && Array.isArray(response.data)) {
        const entries = response.data;
        if (entries.length === 0) {
          const empty = document.createElement('p');
          empty.className = 'feed-empty-msg';
          empty.textContent = 'No friends active yet. Share your link!';
          feedContainer.replaceChildren(empty);
          return;
        }
        feedContainer.replaceChildren();
        entries.forEach(entry => {
          const item = document.createElement('div');
          item.className = 'feed-item';
          if (entry.streams && entry.streams.length > 0) {
            item.addEventListener('click', () => window.open(`https://www.twitch.tv/${encodeURIComponent(entry.streams[0].id)}`, '_blank'));
          }

          const avatar = document.createElement('img');
          avatar.className = 'feed-avatar';
          avatar.alt = entry.display_name || '';
          const rawAvatar = entry.avatar_url || '';
          avatar.src = /^https:\/\/(static-cdn\.jtvnw\.net|[^/]*\.twitch\.tv)\//.test(rawAvatar)
            ? rawAvatar
            : 'https://www.twitch.tv/favicon.ico';

          const content = document.createElement('div');
          content.className = 'feed-content';

          const userInfo = document.createElement('div');
          userInfo.className = 'feed-user-info';
          const userName = document.createElement('span');
          userName.className = 'feed-user-name';
          userName.textContent = entry.display_name || '';
          userInfo.appendChild(userName);

          const activity = document.createElement('div');
          activity.className = 'feed-activity';
          if (entry.streams && entry.streams.length > 0) {
            const group = document.createElement('div');
            group.className = 'feed-activity-group';
            const watching = document.createElement('div');
            watching.className = 'feed-activity';
            watching.textContent = 'is watching';
            group.appendChild(watching);
            const s = entry.streams[0];
            if (s.title) {
              const t = document.createElement('div');
              t.className = 'feed-stream-meta';
              t.textContent = s.title;
              group.appendChild(t);
            }
            if (s.game_name) {
              const g = document.createElement('div');
              g.className = 'feed-stream-meta';
              g.textContent = s.game_name;
              group.appendChild(g);
            }
            activity.replaceWith(group);
          } else {
            activity.textContent = 'is offline';
          }

          content.appendChild(userInfo);
          content.appendChild(activity);
          item.appendChild(avatar);
          item.appendChild(content);
          feedContainer.appendChild(item);
        });
      } else {
        feedContainer.innerHTML = '<p class="feed-empty-msg">Activity currently unavailable.</p>';
      }
    });
  }

  function updateStatusUI() {
    chrome.runtime.sendMessage({ type: 'GET_CURRENT_STATUS' }, (response) => {
      if (chrome.runtime.lastError) return;
      if (response && response.status === 'ok' && response.state && response.state.channelName) {
        watchingText.textContent = `Watching: ${response.state.channelName}`;
        pulseContainer.style.display = 'flex';
      } else {
        watchingText.textContent = 'Not watching anything';
        pulseContainer.style.display = 'none';
      }
    });
  }

  updateStatusUI();
  setInterval(updateStatusUI, 5000);
  setInterval(loadFeed, 30000);

  function updateAuthButtons(isAuthed) {
    loginTwitchBtn.style.display = isAuthed ? 'none' : 'inline-block';
    logoutTwitchBtn.style.display = isAuthed ? 'inline-block' : 'none';
  }

  // Login with Twitch
  loginTwitchBtn.addEventListener('click', () => {
    const clientId = clientIdInput.value.trim();
    if (clientId) chrome.storage.local.set({ TWITCH_CLIENT_ID: clientId });
    loginTwitchBtn.disabled = true;
    loginTwitchBtn.textContent = 'Logging in...';
    chrome.runtime.sendMessage({ type: 'LOGIN_TWITCH' }, (response) => {
      loginTwitchBtn.disabled = false;
      loginTwitchBtn.textContent = 'Login with Twitch';
      if (chrome.runtime.lastError) {
        showStatus(chrome.runtime.lastError.message, 'error');
        showToast(chrome.runtime.lastError.message, 'error');
        return;
      }
      if (response && response.status === 'success') {
        if (response.data && response.data.user_id) userIdInput.value = response.data.user_id;
        showStatus('Logged in with Twitch.', 'success');
        showToast('Logged in with Twitch.', 'success');
        updateAuthButtons(true);
        loadUserProfile();
        loadFeed();
      } else {
        showStatus(response ? response.error : 'Login failed.', 'error');
        showToast(response ? response.error : 'Login failed.', 'error');
      }
    });
  });

  // Logout
  logoutTwitchBtn.addEventListener('click', () => {
    chrome.runtime.sendMessage({ type: 'LOGOUT_TWITCH' }, () => {
      authTokenInput.value = '';
      userIdInput.value = '';
      channelInput.value = '';
      updateAuthButtons(false);
      userDisplay.textContent = 'Not signed in';
      userAvatar.src = 'https://www.twitch.tv/favicon.ico';
      showStatus('Logged out.', 'success');
      showToast('Logged out. Data purged.', 'success');
      updateStatusUI();
    });
  });

  // Save settings
  saveSettingsBtn.addEventListener('click', () => {
    const authToken = authTokenInput.value.trim();
    const userId = userIdInput.value.trim();
    const clientId = clientIdInput.value.trim();
    const consentBox = document.getElementById('privacy-consent');
    const consent = consentBox ? consentBox.checked : false;
    if (clientId) chrome.storage.local.set({ TWITCH_CLIENT_ID: clientId });
    if (consent) chrome.storage.local.set({ PRIVACY_CONSENT: true, PRIVACY_CONSENT_AT: Date.now() });
    if (!authToken || !userId) {
      showStatus('Please provide both Auth Token and User ID (or use Login with Twitch).', 'error');
      return;
    }
    chrome.storage.local.set({ AUTH_TOKEN: authToken, USER_ID: userId }, () => {
      showStatus('Settings saved!', 'success');
      showToast('Settings saved.', 'success');
      updateAuthButtons(true);
      loadUserProfile();
      loadFeed();
    });
  });

  if (openOptionsLink) {
    openOptionsLink.addEventListener('click', (e) => {
      e.preventDefault();
      if (chrome.runtime.openOptionsPage) chrome.runtime.openOptionsPage();
    });
  }

  // Use current tab
  if (useTabBtn) {
    useTabBtn.addEventListener('click', async () => {
      try {
        const tabs = await chrome.tabs.query({ active: true, currentWindow: true });
        const tab = tabs && tabs[0];
        const channel = tab && tab.url ? channelFromUrl(tab.url) : null;
        if (channel && channelInput) {
          channelInput.value = channel;
          updateSyncButtonState();
          showToast('Imported channel from current tab.', 'success');
        } else {
          showToast('Current tab is not a Twitch channel page.', 'error');
        }
      } catch (e) {
        showToast('Could not read current tab: ' + e.message, 'error');
      }
    });
  }

  // Sync Now
  syncBtn.addEventListener('click', () => {
    const channelName = normalizeChannel(channelInput.value);
    if (!isValidChannel(channelInput.value)) {
      showStatus('Please enter a valid channel name or Twitch URL.', 'error');
      return;
    }
    syncBtn.disabled = true;
    const originalText = syncBtn.textContent;
    syncBtn.textContent = 'Syncing...';
    chrome.runtime.sendMessage({ type: 'SYNC_NOW', payload: { channelName } }, (response) => {
      syncBtn.disabled = false;
      syncBtn.textContent = originalText;
      updateSyncButtonState();
      if (chrome.runtime.lastError) {
        showStatus('Error: ' + chrome.runtime.lastError.message, 'error');
        showToast('Sync failed. Check connection.', 'error');
      } else if (response && response.status === 'success') {
        watchingText.textContent = `Watching: ${channelName}`;
        pulseContainer.style.display = 'flex';
        const now = Date.now();
        chrome.storage.local.set({ LAST_SYNCED: now });
        updateLastSynced(now);
        pushRecentChannel(channelName);
        showStatus('Sync successful!', 'success');
        showToast('Sync successful!', 'success');
        loadFeed();
      } else if (response && response.status === 'error') {
        showStatus('Sync failed: ' + response.error, 'error');
        showToast('Sync failed: ' + response.error, 'error');
      } else {
        showStatus('Sync unknown status.', 'error');
      }
    });
  });

  if (channelInput) {
    channelInput.addEventListener('input', updateSyncButtonState);
  }

  // Copy Public Link
  const copyLinkBtn = document.getElementById('copy-link-btn');
  if (copyLinkBtn) {
    copyLinkBtn.addEventListener('click', async () => {
      const settings = await chrome.storage.local.get(['USER_ID', 'API_BASE']);
      if (!settings.USER_ID) {
        showStatus('User ID not configured.', 'error');
        return;
      }
      const baseUrl = (settings.API_BASE || 'http://localhost:8080').replace(/\/$/, '');
      const publicUrl = `${baseUrl}/u/${settings.USER_ID}`;
      try {
        await navigator.clipboard.writeText(publicUrl);
        showStatus('Link copied!', 'success');
        showToast('Link copied!', 'success');
      } catch (err) {
        showStatus('Failed to copy link.', 'error');
      }
    });
  }
});

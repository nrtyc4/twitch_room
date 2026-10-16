/**
 * Twitch Room Popup Script
 * Manages the popup UI and interactions.
 */

document.addEventListener('DOMContentLoaded', async () => {
  const watchingText = document.getElementById('watching-text');
  const syncBtn = document.getElementById('sync-btn');
  const copyLinkBtn = document.getElementById('copy-link-btn');
  const saveSettingsBtn = document.getElementById('save-settings-btn');
  const authTokenInput = document.getElementById('auth-token');
  const userIdInput = document.getElementById('user-id');
  const statusMessage = document.getElementById('status-message');
  const pulseContainer = document.getElementById('pulse-container');
  const userAvatar = document.getElementById('user-avatar');
  const feedContainer = document.getElementById('feed-container');

  // Load current settings from storage
  chrome.storage.local.get(['AUTH_TOKEN', 'USER_ID'], (result) => {
    if (result.AUTH_TOKEN) authTokenInput.value = result.AUTH_TOKEN;
    if (result.USER_ID) userIdInput.value = result.USER_ID;
    loadUserProfile();
    loadFeed();
  });

  /**
   * Fetches and displays the user's profile information.
   */
  async function loadUserProfile() {
    chrome.runtime.sendMessage({ type: 'GET_USER_INFO' }, (response) => {
      if (chrome.runtime.lastError) {
        console.error('Error getting user info:', chrome.runtime.lastError.message);
        return;
      }

      if (response && response.status === 'success' && response.data) {
        if (response.data.avatar_url) {
          userAvatar.src = response.data.avatar_url;
        }
      } else {
        console.log('User info not available (maybe dev mode or not authenticated)');
      }
    });
  }

  /**
   * Fetches and renders the social feed.
   */
  async function loadFeed() {
    chrome.runtime.sendMessage({ type: 'GET_FEED' }, (response) => {
      if (chrome.runtime.lastError) {
        console.error('Error getting feed:', chrome.runtime.lastError.message);
        feedContainer.innerHTML = '<p class="feed-empty-msg">Error loading feed.</p>';
        return;
      }

      if (response && response.status === 'success' && response.data) {
        const entries = response.data;
        if (entries.length === 0) {
          feedContainer.innerHTML = '<p class="feed-empty-msg">No recent activity.</p>';
          return;
        }

        feedContainer.innerHTML = '';
        entries.forEach(entry => {
          const item = document.createElement('div');
          item.className = 'feed-item';
          
          // Make it clickable to go to the first stream if available
          if (entry.streams && entry.streams.length > 0) {
            item.onclick = () => {
              window.open(`https://www.twitch.tv/${entry.streams[0].id}`, '_blank');
            };
          }

          const avatarUrl = entry.avatar_url || 'https://www.twitch.tv/favicon.ico';

          let activityHTML = '<div class="feed-activity">is offline</div>';
          if (entry.streams && entry.streams.length > 0) {
            const s = entry.streams[0];
            const title = s.title ? `<div class="feed-stream-meta">${s.title}</div>` : '';
            const game = s.game_name ? `<div class="feed-stream-meta">${s.game_name}</div>` : '';
            activityHTML = `
              <div class="feed-activity-group">
                <div class="feed-activity">is watching</div>
                ${title}
                ${game}
              </div>
            `;
          }

          item.innerHTML = `
            <img src="${avatarUrl}" class="feed-avatar" alt="${entry.display_name}">
            <div class="feed-content">
              <div class="feed-user-info">
                <span class="feed-user-name">${entry.display_name}</span>
              </div>
              ${activityHTML}
            </div>
          `;
          feedContainer.appendChild(item);
        });
      } else {
        feedContainer.innerHTML = '<p class="feed-empty-msg">Activity currently unavailable.</p>';
      }
    });
  }

  /**
   * Displays a temporary status message in the UI.
   */
  const showStatus = (text, type = 'info') => {
    statusMessage.textContent = text;
    statusMessage.className = 'status-message';
    if (type === 'error') statusMessage.classList.add('error-text');
    if (type === 'success') statusMessage.classList.add('success-text');

    setTimeout(() => {
      statusMessage.textContent = '';
      statusMessage.className = 'status-message';
    }, 3000);
  };

  /**
   * Updates the UI with the current watching status.
   */
  const updateStatusUI = () => {
    chrome.runtime.sendMessage({ type: 'GET_CURRENT_STATUS' }, (response) => {
      if (chrome.runtime.lastError) {
        console.error('Error getting status:', chrome.runtime.lastError.message);
        return;
      }

      if (response && response.status === 'ok' && response.state && response.state.channelName) {
        watchingText.textContent = `Watching: ${response.state.channelName}`;
        pulseContainer.style.display = 'flex';
      } else {
        watchingText.textContent = 'Not watching anything';
        pulseContainer.style.display = 'none';
      }
    });
  };

  // Initial UI update
  updateStatusUI();

  // Autosync/Refresh UI every 5 seconds while popup is open
  setInterval(updateStatusUI, 5000);
  setInterval(loadFeed, 30000); // Refresh feed every 30 seconds

  // Save settings handler
  saveSettingsBtn.addEventListener('click', () => {
    const authToken = authTokenInput.value.trim();
    const userId = userIdInput.value.trim();

    if (!authToken || !userId) {
      showStatus('Please provide both Auth Token and User ID.', 'error');
      return;
    }

    chrome.storage.local.set({ AUTH_TOKEN: authToken, USER_ID: userId }, () => {
      showStatus('Settings saved!', 'success');
      loadUserProfile();
      loadFeed();
    });
  });

  // Sync Now handler
  syncBtn.addEventListener('click', () => {
    syncBtn.disabled = true;
    const originalText = syncBtn.textContent;
    syncBtn.textContent = 'Syncing...';
    
    chrome.runtime.sendMessage({ type: 'SYNC_NOW' }, (response) => {
      syncBtn.disabled = false;
      syncBtn.textContent = originalText;
      
      if (chrome.runtime.lastError) {
        showStatus('Error: ' + chrome.runtime.lastError.message, 'error');
      } else if (response && response.status === 'success') {
        showStatus('Sync successful!', 'success');
        loadFeed(); // Refresh feed after sync
      } else if (response && response.status === 'error') {
        showStatus('Sync failed: ' + response.error, 'error');
      } else {
        showStatus('Sync unknown status.', 'error');
      }
    });
  });

  // Copy Public Link handler
  copyLinkBtn.addEventListener('click', async () => {
    const settings = await chrome.storage.local.get(['USER_ID']);
    if (!settings.USER_ID) {
      showStatus('User ID not configured.', 'error');
      return;
    }

    // We use the origin where the extension would be communicating with.
    // In development, it's localhost:8080.
    const baseUrl = 'http://localhost:8080'; 
    const publicUrl = `${baseUrl}/u/${settings.USER_ID}`;

    try {
      await navigator.clipboard.writeText(publicUrl);
      showStatus('Link copied!', 'success');
    } catch (err) {
      showStatus('Failed to copy link.', 'error');
    }
  });
});

document.addEventListener('DOMContentLoaded', () => {
  const clientIdInput = document.getElementById('client-id');
  const apiBaseInput = document.getElementById('api-base');
  const authStatus = document.getElementById('auth-status');
  const status = document.getElementById('status');
  const saveBtn = document.getElementById('save-btn');
  const testBtn = document.getElementById('test-btn');
  const loginBtn = document.getElementById('login-btn');
  const logoutBtn = document.getElementById('logout-btn');
  const deleteDataBtn = document.getElementById('delete-data-btn');

  function showStatus(msg, ok) {
    status.textContent = msg;
    status.className = 'status ' + (ok ? 'ok' : 'err');
  }

  function refreshAuthStatus() {
    chrome.storage.local.get(['USER_ID', 'AUTH_TOKEN'], (r) => {
      if (r.USER_ID && r.AUTH_TOKEN) {
        authStatus.textContent = `Logged in as ${r.USER_ID}`;
        logoutBtn.style.display = 'inline-block';
        loginBtn.style.display = 'none';
      } else {
        authStatus.textContent = 'Not logged in.';
        logoutBtn.style.display = 'none';
        loginBtn.style.display = 'inline-block';
      }
    });
  }

  chrome.storage.local.get(['TWITCH_CLIENT_ID', 'API_BASE'], (r) => {
    if (r.TWITCH_CLIENT_ID) clientIdInput.value = r.TWITCH_CLIENT_ID;
    if (r.API_BASE) apiBaseInput.value = r.API_BASE;
    else apiBaseInput.value = 'http://localhost:8080';
    refreshAuthStatus();
  });

  saveBtn.addEventListener('click', async () => {
    const clientId = clientIdInput.value.trim();
    const apiBase = apiBaseInput.value.trim() || 'http://localhost:8080';
    let origin;
    try { origin = new URL(apiBase).origin + '/*'; } catch (e) { origin = null; }
    if (origin && !/^https?:\/\/localhost(:\d+)?\/\*$/.test(origin)) {
      const granted = await new Promise((resolve) => {
        chrome.permissions.request({ origins: [origin] }, (g) => resolve(!!g));
      });
      if (!granted) {
        showStatus('Host permission denied; keeping previous base.', false);
        return;
      }
    }
    chrome.storage.local.set({ TWITCH_CLIENT_ID: clientId, API_BASE: apiBase }, () => {
      showStatus('Settings saved.', true);
    });
  });

  loginBtn.addEventListener('click', () => {
    const clientId = clientIdInput.value.trim();
    if (clientId) chrome.storage.local.set({ TWITCH_CLIENT_ID: clientId });
    chrome.runtime.sendMessage({ type: 'LOGIN_TWITCH' }, (resp) => {
      if (chrome.runtime.lastError) {
        showStatus(chrome.runtime.lastError.message, false);
        return;
      }
      if (resp && resp.status === 'success') {
        showStatus('Logged in with Twitch.', true);
        refreshAuthStatus();
      } else {
        showStatus(resp ? resp.error : 'Login failed.', false);
      }
    });
  });

  logoutBtn.addEventListener('click', () => {
    chrome.runtime.sendMessage({ type: 'LOGOUT_TWITCH' }, () => {
      showStatus('Logged out.', true);
      refreshAuthStatus();
    });
  });

  if (deleteDataBtn) {
    deleteDataBtn.addEventListener('click', async () => {
      const r = await chrome.storage.local.get(['API_BASE', 'AUTH_TOKEN', 'USER_ID']);
      const apiBase = (r.API_BASE || 'http://localhost:8080').replace(/\/$/, '');
      try {
        const resp = await fetch(`${apiBase}/v1/account`, {
          method: 'DELETE',
          headers: { 'Authorization': `Bearer ${r.AUTH_TOKEN || ''}`, 'X-User-ID': r.USER_ID || '' },
          credentials: 'include',
        });
        if (resp.ok) showStatus('All data deleted.', true);
        else { showStatus('Delete failed: ' + resp.status, false); return; }
      } catch (e) {
        showStatus('Delete error: ' + e.message, false);
      }
      chrome.runtime.sendMessage({ type: 'LOGOUT_TWITCH' }, () => refreshAuthStatus());
    });
  }

  testBtn.addEventListener('click', async () => {
    const apiBase = apiBaseInput.value.trim() || 'http://localhost:8080';
    try {
      const res = await fetch(`${apiBase}/health`);
      if (res.ok) showStatus('Connection OK.', true);
      else showStatus(`Connection failed: ${res.status}`, false);
    } catch (e) {
      showStatus(`Connection error: ${e.message}`, false);
    }
  });
});
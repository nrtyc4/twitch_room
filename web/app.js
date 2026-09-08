"use strict";

const $ = (id) => document.getElementById(id);
let sessionUser = null;
let currentUser = localStorage.getItem("twitch-room-user") || "";
let usingSession = false;

const params = new URLSearchParams(window.location.search);
const oauthUser = params.get("user_id");
if (oauthUser) {
  currentUser = oauthUser;
  localStorage.setItem("twitch-room-user", oauthUser);
  history.replaceState(null, "", "/");
}

async function fetchMe() {
  const resp = await fetch("/auth/me");
  if (resp.status === 200) {
    try {
      const data = await resp.json();
      if (data.user_id) sessionUser = data.user_id;
    } catch (e) { /* ignore */ }
  }
}

function setIdentity(userID) {
  const trimmed = (userID || "").trim();
  if (!trimmed) return;
  currentUser = trimmed;
  usingSession = false;
  sessionUser = null;
  localStorage.setItem("twitch-room-user", currentUser);
  $("user-id").value = currentUser;
  updateIdentityBar();
  loadAll();
}

function updateIdentityBar() {
  const loggedIn = Boolean(usingSession && sessionUser);
  $("twitch-login").hidden = loggedIn;
  $("logout").hidden = !loggedIn;
  $("user-id").hidden = loggedIn;
  $("identity-set").hidden = loggedIn;
  $("user-id").placeholder = loggedIn
    ? `Signed in as ${sessionUser}`
    : "your user id";
}

async function logout() {
  await api("POST", "/auth/logout");
  sessionUser = null;
  usingSession = false;
  currentUser = "";
  localStorage.removeItem("twitch-room-user");
  updateIdentityBar();
  location.reload();
}

function fmtTime(iso) {
  const d = new Date(iso);
  if (isNaN(d)) return "";
  return d.toLocaleTimeString();
}

async function api(method, path, body) {
  const headers = {};
  if (usingSession && sessionUser) {
    headers["X-User-ID"] = sessionUser;
  } else if (currentUser) {
    headers["X-User-ID"] = currentUser;
  }
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const resp = await fetch(path, {
    method,
    headers,
    credentials: "same-origin",
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (resp.status === 401) {
    showError("Authentication required: set a user id above.");
    return null;
  }
  if (!resp.ok) {
    let message = `${resp.status}`;
    try {
      const data = await resp.json();
      if (data.error) message = data.error;
    } catch (e) { /* ignore */ }
    showError(message);
    return null;
  }
  if (resp.status === 204) return null;
  return resp.json();
}

let errorTimer = null;
function showError(message) {
  const el = $("error");
  el.textContent = message;
  el.hidden = false;
  clearTimeout(errorTimer);
  errorTimer = setTimeout(() => { el.hidden = true; }, 4000);
}

function streamIdFromUrl(value) {
  const t = value.trim();
  if (!t) return "";
  const m = t.match(/twitch\.tv\/([A-Za-z0-9_]+)/);
  return m ? m[1] : t.split(/[\s,]+/)[0];
}

function loadWatchInputFromState(state) {
  if (!state) return;
  $("watch-input").value = (state.stream_ids || []).join("\n");
  $("watch-current").innerHTML =
    `<span class="meta">Updated ${fmtTime(state.updated_at)}</span>`;
}

function renderStreams(streams) {
  if (!streams || streams.length === 0) return "";
  return `<div class="streams">${streams
    .map((s) => {
      const name = s.channel_name || s.id || "unknown";
      const thumb = s.thumbnail_url
        ? `<img class="thumb" src="${escapeHtml(s.thumbnail_url)}" alt="" loading="lazy" />`
        : `<div class="thumb placeholder"></div>`;
      const live = s.is_live
        ? `<span class="badge live">LIVE${s.viewer_count ? " \u00b7 " + s.viewer_count : ""}</span>`
        : `<span class="badge off">offline</span>`;
      return `
        <div class="stream">
          ${thumb}
          <div class="info">
            <div class="name">${escapeHtml(name)} ${live}</div>
            ${s.title ? `<div class="stream-title">${escapeHtml(s.title)}</div>` : ""}
            ${s.game_name ? `<div class="meta">${escapeHtml(s.game_name)}</div>` : ""}
          </div>
        </div>`;
    })
    .join("")}</div>`;
}

function escapeHtml(value) {
  return String(value)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

async function loadFeed() {
  const feed = await api("GET", "/v1/feed");
  const el = $("feed");
  if (feed === null) return;
  if (feed.length === 0) {
    el.innerHTML = `<div class="empty">Nothing yet. Follow people in the People tab.</div>`;
    return;
  }
  el.innerHTML = feed
    .map(
      (entry) => `
      <div class="card">
        <div class="head">
          <span class="user">${escapeHtml(entry.display_name || entry.user_id)}</span>
          <div>
            <span class="meta">${fmtTime(entry.updated_at)}</span>
            <button class="secondary" data-action="unfollow" data-id="${escapeHtml(entry.user_id)}">Unfollow</button>
          </div>
        </div>
        ${renderStreams(entry.streams)}
      </div>`
    )
    .join("");
  bindCardActions(el);
}

async function loadWatch() {
  const state = await api("GET", "/v1/watch");
  if (state === null) return;
  loadWatchInputFromState(state);
}

async function saveWatch() {
  const lines = $("watch-input").value.split("\n");
  const ids = [...new Set(lines.map(streamIdFromUrl).filter(Boolean))];
  const state = await api("POST", "/v1/watch", { stream_ids: ids });
  if (state) loadWatchInputFromState(state);
}

async function clearWatch() {
  await api("DELETE", "/v1/watch");
  $("watch-input").value = "";
  $("watch-current").textContent = "";
}

async function loadPeople() {
  const subs = await api("GET", "/v1/subscriptions");
  const el = $("following");
  if (subs === null) return;
  if (subs.length === 0) {
    el.innerHTML = `<div class="empty">You are not following anyone yet.</div>`;
    return;
  }
  el.innerHTML = subs
    .map(
      (sub) => `
      <div class="card">
        <div class="head">
          <span class="user">${escapeHtml(sub.publisher_id)}</span>
          <div>
            <span class="badge ${sub.status}">${escapeHtml(sub.status)}</span>
            <button data-action="unfollow" data-id="${escapeHtml(sub.publisher_id)}">Unfollow</button>
          </div>
        </div>
      </div>`
    )
    .join("");
  bindCardActions(el);
}

async function followUser() {
  const id = $("follow-input").value.trim();
  if (!id) return;
  const sub = await api("POST", `/v1/subscriptions/${encodeURIComponent(id)}`);
  $("follow-input").value = "";
  if (sub && sub.status === "pending") {
    showError("Request sent - waiting for approval.");
  }
  loadPeople();
}

async function unfollow(id) {
  await api("DELETE", `/v1/subscriptions/${encodeURIComponent(id)}`);
  loadPeople();
  loadFeed();
}

async function loadRequests() {
  const requests = await api("GET", "/v1/subscription_requests");
  const el = $("requests");
  if (requests === null) {
    renderRequestBadge(0);
    return;
  }
  renderRequestBadge(requests.length);
  if (requests.length === 0) {
    const mode = $("setting-mode").value;
    el.innerHTML = mode === "open"
      ? `<div class="empty">No pending requests. You are in <b>Open</b> mode and follow requests never queue up — switch to <b>Approved</b> in Settings to require approval.</div>`
      : `<div class="empty">No pending requests.</div>`;
    return;
  }
  el.innerHTML = requests
    .map(
      (req) => `
      <div class="card">
        <div class="head">
          <span class="user">${escapeHtml(req.subscriber_id)} wants to follow you</span>
          <div>
            <span class="meta">requested ${fmtTime(req.created_at)}</span>
            <button class="danger" data-action="reject" data-id="${escapeHtml(req.subscriber_id)}">Reject</button>
            <button data-action="approve" data-id="${escapeHtml(req.subscriber_id)}">Approve</button>
          </div>
        </div>
      </div>`
    )
    .join("");
  el.querySelectorAll("[data-action=approve]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      await api("POST", `/v1/subscription_requests/${encodeURIComponent(btn.dataset.id)}/approve`);
      loadRequests();
    });
  });
  el.querySelectorAll("[data-action=reject]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      await api("POST", `/v1/subscription_requests/${encodeURIComponent(btn.dataset.id)}/reject`);
      loadRequests();
    });
  });
}

function renderRequestBadge(count) {
  const badge = $("request-count");
  if (count <= 0) {
    badge.hidden = true;
    badge.textContent = "";
    return;
  }
  badge.hidden = false;
  badge.textContent = count > 99 ? "99+" : String(count);
}

async function loadSettings() {
  const settings = await api("GET", "/v1/settings");
  if (settings === null) return;
  $("setting-mode").value = settings.subscription_mode || "open";
  $("setting-visibility").value = settings.visibility || "subscribers";
  const me = (usingSession && sessionUser) || currentUser;
  const visible = settings.visibility === "public" && Boolean(me);
  $("share-link-wrap").hidden = !visible;
  if (visible) {
    $("share-link").textContent = "/u/" + encodeURIComponent(me);
    $("share-link").href = "/u/" + encodeURIComponent(me);
  }
}

async function saveSettings() {
  const settings = {
    subscription_mode: $("setting-mode").value,
    visibility: $("setting-visibility").value,
  };
  const saved = await api("PUT", "/v1/settings", settings);
  if (saved) {
    $("settings-status").textContent = `Saved: ${saved.subscription_mode} / ${saved.visibility}`;
    loadSettings();
  }
}

function bindCardActions(container) {
  container.querySelectorAll("[data-action=unfollow]").forEach((btn) => {
    btn.addEventListener("click", () => unfollow(btn.dataset.id));
  });
}

function loadAll() {
  const user = usingSession && sessionUser ? sessionUser : currentUser;
  if (!user) return;
  loadFeed();
  loadWatch();
  loadPeople();
  loadRequests();
  loadSettings();
}

// Tabs
document.querySelectorAll(".tab").forEach((tab) => {
  tab.addEventListener("click", () => {
    document.querySelectorAll(".tab").forEach((t) => t.classList.remove("active"));
    document.querySelectorAll(".panel").forEach((p) => p.classList.remove("active"));
    tab.classList.add("active");
    $(`tab-${tab.dataset.tab}`).classList.add("active");
  });
});

$("identity-set").addEventListener("click", () => setIdentity($("user-id").value));
$("user-id").addEventListener("keydown", (e) => {
  if (e.key === "Enter") setIdentity($("user-id").value);
});
$("logout").addEventListener("click", logout);
$("watch-save").addEventListener("click", saveWatch);
$("watch-clear").addEventListener("click", clearWatch);
$("follow-btn").addEventListener("click", followUser);
$("follow-input").addEventListener("keydown", (e) => {
  if (e.key === "Enter") followUser();
});
$("settings-save").addEventListener("click", saveSettings);

async function init() {
  await fetchMe();
  if (sessionUser) {
    usingSession = true;
    currentUser = sessionUser;
  }
  updateIdentityBar();
  $("user-id").value = currentUser;

  if (currentUser) {
    loadAll();
  } else {
    $("feed").innerHTML = `<div class="empty">Login with Twitch or enter a user id above to get started.</div>`;
  }
}

function hasIdentity() {
  return Boolean((usingSession && sessionUser) || currentUser);
}

setInterval(() => {
  if (hasIdentity()) loadFeed();
}, 15000);
setInterval(() => {
  if (hasIdentity()) loadRequests();
}, 20000);
init();
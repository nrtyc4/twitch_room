// Theme toggle
(function() {
  const saved = localStorage.getItem('wr-theme');
  if (saved === 'light') document.body.classList.add('light');
  const themeBtn = document.getElementById('theme-btn');
  if (themeBtn) {
    themeBtn.addEventListener('click', () => {
      document.body.classList.toggle('light');
      localStorage.setItem('wr-theme', document.body.classList.contains('light') ? 'light' : 'dark');
    });
  }
})();

// Client-side refresh
document.addEventListener('DOMContentLoaded', () => {
  const refreshBtn = document.getElementById('refresh-btn');
  if (refreshBtn) {
    refreshBtn.addEventListener('click', async () => {
      try {
        const res = await fetch(location.href, { cache: 'no-store' });
        if (!res.ok) throw new Error(res.status);
        const html = await res.text();
        const doc = new DOMParser().parseFromString(html, 'text/html');
        const newMain = doc.querySelector('main');
        if (newMain) document.querySelector('main').replaceWith(newMain);
      } catch (e) {
        console.error('Refresh failed', e);
      }
    });
  }
});

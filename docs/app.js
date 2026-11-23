(function () {
  var root = document.documentElement;
  var themes = ['system', 'mocha', 'latte'];
  var themeButtons = Array.prototype.slice.call(document.querySelectorAll('[data-theme-set]'));

  function applyTheme(t) {
    if (themes.indexOf(t) === -1) t = 'system';
    root.dataset.theme = t;
    try { localStorage.setItem('agentbar.theme', t); } catch (e) {}
    themeButtons.forEach(function (btn) {
      btn.setAttribute('aria-pressed', btn.dataset.themeSet === t ? 'true' : 'false');
    });
  }

  applyTheme(root.dataset.theme || 'system');

  themeButtons.forEach(function (btn) {
    btn.addEventListener('click', function () {
      applyTheme(btn.dataset.themeSet);
    });
  });

  // Live macOS menu bar clock
  var clockEl = document.getElementById('menubarClock');
  function updateClock() {
    if (!clockEl) return;
    var now = new Date();
    var day = now.toLocaleDateString(undefined, { weekday: 'short' });
    var time = now.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', hour12: false });
    clockEl.textContent = day + ' ' + time;
  }
  updateClock();
  setInterval(updateClock, 30000);

  // Interactive HUD: collapse/expand provider cards
  document.querySelectorAll('.p-card .p-toggle').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var card = btn.closest('.p-card');
      var collapsed = card.classList.toggle('is-collapsed');
      btn.setAttribute('aria-expanded', collapsed ? 'false' : 'true');
      var chev = btn.querySelector('.chev');
      if (chev) chev.textContent = collapsed ? '›' : '⌄';
    });
  });

  // HUD summon / hide (⌥⇧A, Esc, or menubar robot button)
  var hud = document.getElementById('liveHud');
  var summonHint = document.getElementById('summonHint');
  var menubarToggle = document.getElementById('menubarToggle');

  function setHudVisible(visible) {
    if (!hud) return;
    hud.classList.toggle('is-hidden', !visible);
    if (summonHint) summonHint.hidden = visible;
    if (menubarToggle) menubarToggle.classList.toggle('is-active', visible);
  }

  if (menubarToggle) {
    menubarToggle.addEventListener('click', function () {
      setHudVisible(hud.classList.contains('is-hidden'));
    });
  }
  if (summonHint) {
    summonHint.addEventListener('click', function () { setHudVisible(true); });
  }

  // Refresh micro-interaction
  var refreshBtn = document.getElementById('hudRefresh');
  var statusEl = document.getElementById('hudStatus');
  function triggerRefresh() {
    if (!refreshBtn || refreshBtn.classList.contains('spinning')) return;
    refreshBtn.classList.add('spinning');
    if (statusEl) statusEl.textContent = 'Refreshing…';
    setTimeout(function () {
      refreshBtn.classList.remove('spinning');
      if (statusEl) statusEl.textContent = 'Updated just now';
    }, 650);
  }
  if (refreshBtn) refreshBtn.addEventListener('click', triggerRefresh);

  // Settings icon inside HUD cycles themes
  var hudThemeCycle = document.getElementById('hudThemeCycle');
  if (hudThemeCycle) {
    hudThemeCycle.addEventListener('click', function () {
      var cur = root.dataset.theme || 'system';
      var next = themes[(themes.indexOf(cur) + 1) % themes.length];
      applyTheme(next);
    });
  }

  // Global keyboard shortcuts: ⌥⇧A toggles HUD, Esc hides, ⌘R refreshes
  window.addEventListener('keydown', function (e) {
    if (e.altKey && e.shiftKey && (e.code === 'KeyA' || e.key.toLowerCase() === 'a')) {
      e.preventDefault();
      setHudVisible(hud.classList.contains('is-hidden'));
    } else if (e.key === 'Escape' && hud && !hud.classList.contains('is-hidden')) {
      setHudVisible(false);
    } else if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'r' && hud && !hud.classList.contains('is-hidden')) {
      e.preventDefault();
      triggerRefresh();
    }
  });

  // Copy SQL button
  document.querySelectorAll('.copy-btn').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var target = document.querySelector(btn.dataset.copy);
      if (!target) return;
      navigator.clipboard.writeText(target.textContent.trim()).then(function () {
        btn.textContent = 'Copied';
        setTimeout(function () { btn.textContent = 'Copy'; }, 1400);
      });
    });
  });

  // Recent updates collapsible card
  var updatesCard = document.getElementById('updatesCard');
  var updatesToggle = document.getElementById('updatesToggle');
  if (updatesCard && updatesToggle) {
    updatesToggle.addEventListener('click', function () {
      var expanded = updatesCard.classList.toggle('is-expanded');
      updatesToggle.setAttribute('aria-expanded', expanded ? 'true' : 'false');
      updatesToggle.setAttribute('aria-label', expanded ? 'Collapse updates' : 'Expand updates');
    });
  }
})();

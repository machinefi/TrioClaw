// Main dashboard controller.

const POLL_INTERVAL = 10000; // 10s

let state = {
  status: null,
  cameras: [],
  watches: [],
  stats: null,
  alerts: [],
  events: [],
  connected: true,
};

let seenAlertIds = new Set();
let eventFilters = { date: todayDate(), camera: '', alertsOnly: false };

function todayDate() {
  const d = new Date();
  return d.getFullYear() + '-' +
    String(d.getMonth() + 1).padStart(2, '0') + '-' +
    String(d.getDate()).padStart(2, '0');
}

// --- Fetch & Update ---

async function fetchSafe(fn) {
  try {
    const result = await fn();
    state.connected = true;
    return result;
  } catch (e) {
    state.connected = false;
    return null;
  }
}

async function refreshAll() {
  const [status, cameras, watches, stats, alerts] = await Promise.all([
    fetchSafe(() => API.status()),
    fetchSafe(() => API.cameras()),
    fetchSafe(() => API.watches()),
    fetchSafe(() => API.stats()),
    fetchSafe(() => API.recentAlerts(50)),
  ]);

  if (status) state.status = status;
  if (cameras) state.cameras = cameras;
  if (watches) state.watches = watches;
  if (stats) state.stats = stats;
  if (alerts) {
    // Track seen IDs for highlight animation
    state.alerts.forEach(a => seenAlertIds.add(a.id));
    state.alerts = alerts;
  }

  await refreshEvents();
  render();
}

async function refreshPolling() {
  const [status, watches, alerts] = await Promise.all([
    fetchSafe(() => API.status()),
    fetchSafe(() => API.watches()),
    fetchSafe(() => API.recentAlerts(50)),
  ]);

  if (status) state.status = status;
  if (watches) state.watches = watches;
  if (alerts) {
    state.alerts.forEach(a => seenAlertIds.add(a.id));
    state.alerts = alerts;
  }

  render();
}

async function refreshEvents() {
  const opts = {
    date: eventFilters.date,
    camera: eventFilters.camera || undefined,
    limit: 200,
  };

  let events;
  if (eventFilters.alertsOnly) {
    events = await fetchSafe(() => API.alerts(opts));
  } else {
    events = await fetchSafe(() => API.events(opts));
  }
  if (events) state.events = events;
}

// --- Render ---

function render() {
  const topbar = document.getElementById('topbar');
  const cards = document.getElementById('status-cards');
  const grid = document.getElementById('camera-grid');
  const feed = document.getElementById('alert-feed-content');
  const timeline = document.getElementById('event-timeline-content');

  if (topbar) topbar.innerHTML = renderTopBar(state.status, state.connected);
  if (cards) cards.innerHTML = renderStatusCards(state.status, state.stats);
  if (grid) grid.innerHTML = renderCameraGrid(state.cameras, state.watches);
  if (feed) feed.innerHTML = renderAlertFeed(state.alerts, seenAlertIds);
  if (timeline) timeline.innerHTML = renderEventTable(state.events);

  // After render, mark all current alerts as seen
  state.alerts.forEach(a => seenAlertIds.add(a.id));
}

// --- Filters ---

function setupFilters() {
  const dateInput = document.getElementById('filter-date');
  const cameraSelect = document.getElementById('filter-camera');
  const alertsToggle = document.getElementById('filter-alerts-only');

  if (dateInput) {
    dateInput.value = eventFilters.date;
    dateInput.addEventListener('change', async () => {
      eventFilters.date = dateInput.value;
      await refreshEvents();
      render();
    });
  }

  if (cameraSelect) {
    cameraSelect.addEventListener('change', async () => {
      eventFilters.camera = cameraSelect.value;
      await refreshEvents();
      render();
    });
  }

  if (alertsToggle) {
    alertsToggle.addEventListener('change', async () => {
      eventFilters.alertsOnly = alertsToggle.checked;
      await refreshEvents();
      render();
    });
  }
}

function updateCameraFilter() {
  const cameraSelect = document.getElementById('filter-camera');
  if (!cameraSelect || !state.cameras) return;

  const current = cameraSelect.value;
  const opts = ['<option value="">All cameras</option>'];
  state.cameras.forEach(c => {
    const sel = c.id === current ? ' selected' : '';
    opts.push(`<option value="${escapeAttr(c.id)}"${sel}>${escapeHTML(c.name)}</option>`);
  });
  cameraSelect.innerHTML = opts.join('');
}

// --- Init ---

async function init() {
  await refreshAll();
  updateCameraFilter();
  setupFilters();
  setInterval(refreshPolling, POLL_INTERVAL);
}

document.addEventListener('DOMContentLoaded', init);

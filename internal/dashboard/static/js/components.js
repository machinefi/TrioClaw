// Render functions for dashboard sections.

function timeAgo(isoString) {
  if (!isoString) return '—';
  const now = Date.now();
  const then = new Date(isoString).getTime();
  const diff = Math.floor((now - then) / 1000);
  if (diff < 0) return 'just now';
  if (diff < 60) return `${diff}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  return `${Math.floor(diff / 86400)}d ago`;
}

function formatTime(isoString) {
  if (!isoString) return '—';
  const d = new Date(isoString);
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

function statusDot(state) {
  const colors = { running: '#3fb950', connecting: '#d29922', watching: '#3fb950' };
  const color = colors[state] || '#f85149';
  const pulse = (state === 'running' || state === 'watching') ? ' pulse' : '';
  return `<span class="status-dot${pulse}" style="--dot-color:${color}"></span>`;
}

function conditionTag(id) {
  return `<span class="tag">${escapeHTML(id)}</span>`;
}

function escapeHTML(str) {
  const div = document.createElement('div');
  div.textContent = str;
  return div.innerHTML;
}

function escapeAttr(str) {
  return escapeHTML(str).replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}

// --- Status Cards ---

function renderStatusCards(status, stats) {
  const todayAlerts = stats && stats.today_by_camera
    ? Object.values(stats.today_by_camera).reduce((a, b) => a + b, 0)
    : 0;

  return `
    <div class="stat-card">
      <div class="stat-label">Cameras</div>
      <div class="stat-value">${status ? status.cameras : '—'}</div>
    </div>
    <div class="stat-card">
      <div class="stat-label">Active Watches</div>
      <div class="stat-value">${status && status.watches ? status.watches.length : '0'}</div>
    </div>
    <div class="stat-card accent-red">
      <div class="stat-label">Today's Alerts</div>
      <div class="stat-value">${todayAlerts}</div>
    </div>
    <div class="stat-card">
      <div class="stat-label">Total Events</div>
      <div class="stat-value">${stats ? stats.total_events.toLocaleString() : '—'}</div>
    </div>
  `;
}

// --- Camera Grid ---

function renderCameraGrid(cameras, watches) {
  if (!cameras || cameras.length === 0) {
    return '<div class="empty-state">No cameras configured. Run <code>trioclaw camera add</code> to get started.</div>';
  }

  const watchMap = {};
  if (watches) {
    watches.forEach(w => { watchMap[w.camera_id] = w; });
  }

  return cameras.map(cam => {
    const watch = watchMap[cam.id];
    const state = watch ? watch.state : 'stopped';
    const stateLabel = watch ? state : 'not watching';
    const conditions = (cam.conditions || []).map(c => conditionTag(c)).join('');

    return `
      <div class="camera-card">
        <div class="camera-header">
          ${statusDot(state)}
          <span class="camera-name">${escapeHTML(cam.name)}</span>
          <span class="camera-state">${stateLabel}</span>
        </div>
        <div class="camera-meta">
          <span class="camera-id">${escapeHTML(cam.id)}</span>
          <span class="camera-fps">${cam.fps} fps</span>
        </div>
        <div class="camera-source">${escapeHTML(cam.source)}</div>
        ${conditions ? `<div class="camera-conditions">${conditions}</div>` : ''}
      </div>
    `;
  }).join('');
}

// --- Alert Feed ---

function renderAlertFeed(alerts, seenIds) {
  if (!alerts || alerts.length === 0) {
    return '<div class="empty-state">No alerts yet. Alerts will appear here when conditions are triggered.</div>';
  }

  return alerts.map(a => {
    const isNew = seenIds && !seenIds.has(a.id);
    return `
      <div class="alert-row${isNew ? ' new-alert' : ''}">
        <span class="alert-time">${timeAgo(a.timestamp)}</span>
        <span class="alert-camera">${escapeHTML(a.camera_id)}</span>
        <span class="alert-condition">${escapeHTML(a.condition_id)}</span>
        <span class="alert-answer">${escapeHTML(a.answer)}</span>
        <span class="alert-latency">${a.latency_ms ? a.latency_ms.toFixed(0) + 'ms' : ''}</span>
      </div>
    `;
  }).join('');
}

// --- Event Timeline ---

function renderEventTable(events) {
  if (!events || events.length === 0) {
    return '<div class="empty-state">No events for this date.</div>';
  }

  const rows = events.map(e => `
    <tr class="${e.triggered ? 'triggered-row' : ''}">
      <td class="mono">${formatTime(e.timestamp)}</td>
      <td>${escapeHTML(e.camera_id)}</td>
      <td>${escapeHTML(e.condition_id)}</td>
      <td class="answer-cell">${escapeHTML(e.answer)}</td>
      <td class="center">${e.triggered ? '<span class="trigger-mark">!</span>' : '—'}</td>
      <td class="mono">${e.latency_ms ? e.latency_ms.toFixed(0) : '—'}</td>
    </tr>
  `).join('');

  return `
    <table class="event-table">
      <thead>
        <tr>
          <th>Time</th>
          <th>Camera</th>
          <th>Condition</th>
          <th>Answer</th>
          <th class="center">Alert</th>
          <th>Latency</th>
        </tr>
      </thead>
      <tbody>${rows}</tbody>
    </table>
  `;
}

// --- Top Bar ---

function renderTopBar(status, connected) {
  const uptime = status ? status.uptime : '—';
  const dotColor = connected ? '#3fb950' : '#f85149';
  const dotClass = connected ? 'pulse' : '';
  const now = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

  return `
    <div class="topbar-left">
      <svg class="topbar-logo" viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg">
        <circle cx="16" cy="16" r="14" stroke="#58a6ff" stroke-width="2.5"/>
        <circle cx="16" cy="16" r="6" fill="#58a6ff"/>
        <circle cx="16" cy="16" r="2.5" fill="#0d1117"/>
      </svg>
      <span class="topbar-title">TrioClaw</span>
    </div>
    <div class="topbar-right">
      <span class="topbar-uptime">up ${escapeHTML(uptime)}</span>
      <span class="topbar-refresh">updated ${now}</span>
      <span class="status-dot ${dotClass}" style="--dot-color:${dotColor}"></span>
    </div>
  `;
}

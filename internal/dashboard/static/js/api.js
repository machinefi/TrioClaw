// API client for TrioClaw REST endpoints.
async function fetchJSON(url) {
  const r = await fetch(url);
  if (!r.ok) throw new Error(`HTTP ${r.status}`);
  return r.json();
}

const API = {
  async status() {
    return fetchJSON('/api/status');
  },

  async cameras() {
    return fetchJSON('/api/cameras');
  },

  async watches() {
    return fetchJSON('/api/watches');
  },

  async stats() {
    return fetchJSON('/api/stats');
  },

  async recentAlerts(limit = 50) {
    return fetchJSON(`/api/alerts/recent?limit=${limit}`);
  },

  async events(opts = {}) {
    const params = new URLSearchParams();
    if (opts.date) params.set('date', opts.date);
    if (opts.camera) params.set('camera', opts.camera);
    if (opts.limit) params.set('limit', opts.limit);
    const qs = params.toString();
    return fetchJSON(`/api/events${qs ? '?' + qs : ''}`);
  },

  async alerts(opts = {}) {
    const params = new URLSearchParams();
    if (opts.date) params.set('date', opts.date);
    if (opts.camera) params.set('camera', opts.camera);
    if (opts.limit) params.set('limit', opts.limit);
    const qs = params.toString();
    return fetchJSON(`/api/alerts${qs ? '?' + qs : ''}`);
  },

  clipURL(filename) {
    return `/api/clips/${encodeURIComponent(filename)}`;
  },
};

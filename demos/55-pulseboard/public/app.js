(() => {
  const API_BASE =
    window.PULSEBOARD_API_BASE ||
    `${window.location.protocol}//api.pulseboard.localhost${
      window.location.port ? `:${window.location.port}` : ''
    }`;

  const statusEl = document.getElementById('status');
  const replicasEl = document.getElementById('replicas');
  const counterEl = document.getElementById('counter');
  const rpsEl = document.getElementById('rps');
  const p95El = document.getElementById('p95');
  const instanceEl = document.getElementById('instance');
  const sourceEl = document.getElementById('source');

  if (!statusEl || !replicasEl || !counterEl || !rpsEl || !p95El || !instanceEl) {
    return;
  }

  async function refresh() {
    try {
      const res = await fetch(`${API_BASE}/stats`, {
        headers: { Accept: 'application/json' },
      });
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`);
      }
      const stats = await res.json();
      replicasEl.textContent = String(stats.replicas ?? '—');
      counterEl.textContent = String(stats.counter ?? '—');
      rpsEl.textContent = Number(stats.rps ?? 0).toFixed(1);
      p95El.textContent = Number(stats.p95Ms ?? 0).toFixed(0);
      instanceEl.textContent = stats.instance || '—';
      if (sourceEl) {
        sourceEl.textContent = stats.source || '—';
      }
      statusEl.textContent = `Observe live · updated ${stats.updatedAt || 'just now'}`;
    } catch (err) {
      statusEl.textContent = `API unavailable: ${err.message}`;
    }
  }

  refresh();
  setInterval(refresh, 1500);
})();

(() => {
  const API_BASE =
    window.ORDERPIPE_API_BASE ||
    `${window.location.protocol}//api.orderpipe.localhost${window.location.port ? `:${window.location.port}` : ''}`;

  const catalogStatus = document.getElementById('catalog-status');
  const catalogList = document.getElementById('catalog-list');
  const skuSelect = document.getElementById('sku');
  const form = document.getElementById('order-form');
  const orderStatus = document.getElementById('order-status');
  const orderResult = document.getElementById('order-result');
  const orderSummary = document.getElementById('order-summary');

  async function api(path, options = {}) {
    const headers = { ...(options.headers || {}) };
    if (options.body && !headers['Content-Type']) {
      headers['Content-Type'] = 'application/json';
    }
    const res = await fetch(`${API_BASE}${path}`, { ...options, headers });
    if (!res.ok) {
      const text = await res.text();
      throw new Error(text || `HTTP ${res.status}`);
    }
    return res.json();
  }

  function money(cents) {
    return `$${(cents / 100).toFixed(2)}`;
  }

  function renderCatalog(items) {
    catalogList.replaceChildren();
    skuSelect.replaceChildren();
    if (!items.length) {
      const empty = document.createElement('p');
      empty.className = 'empty';
      empty.textContent = 'Catalog empty — run seed.sh';
      catalogList.appendChild(empty);
      return;
    }
    for (const item of items) {
      const li = document.createElement('li');
      li.className = 'catalog-item';
      li.textContent = `${item.name} (${item.sku}) — ${money(item.unitCents)}`;
      catalogList.appendChild(li);

      const opt = document.createElement('option');
      opt.value = item.sku;
      opt.textContent = `${item.name} (${item.sku})`;
      skuSelect.appendChild(opt);
    }
  }

  async function refreshCatalog() {
    catalogStatus.textContent = 'Loading catalog…';
    try {
      const body = await api('/catalog');
      renderCatalog(body.items || []);
      catalogStatus.textContent = `${(body.items || []).length} item(s)`;
    } catch (err) {
      catalogStatus.textContent = `Catalog error: ${err.message}`;
    }
  }

  form.addEventListener('submit', async (ev) => {
    ev.preventDefault();
    orderStatus.textContent = 'Placing order…';
    orderResult.hidden = true;
    const email = document.getElementById('customer-email').value.trim();
    const sku = skuSelect.value;
    const qty = Number(document.getElementById('qty').value || '1');
    try {
      const order = await api('/orders', {
        method: 'POST',
        body: JSON.stringify({
          customerEmail: email,
          items: [{ sku, qty }],
        }),
      });
      orderStatus.textContent = 'Order placed.';
      orderSummary.textContent = `${order.id} — ${order.status} — ${money(order.totalCents)} — ${order.customerEmail}`;
      orderResult.hidden = false;
    } catch (err) {
      orderStatus.textContent = `Order failed: ${err.message}`;
    }
  });

  refreshCatalog();
})();

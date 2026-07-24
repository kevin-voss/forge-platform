(() => {
  const API_BASE =
    window.ASKDOCS_API_BASE ||
    `${window.location.protocol}//api.askdocs.localhost${window.location.port ? `:${window.location.port}` : ''}`;

  const SESSION_KEY = 'askdocs.sessionId';
  const form = document.getElementById('chat-form');
  const input = document.getElementById('chat-input');
  const list = document.getElementById('message-list');
  const status = document.getElementById('chat-status');

  if (!form || !input || !list || !status) return;

  function sessionId() {
    let id = localStorage.getItem(SESSION_KEY);
    if (!id) {
      id = `web-${Date.now().toString(36)}`;
      localStorage.setItem(SESSION_KEY, id);
    }
    return id;
  }

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
    if (res.status === 204) return null;
    return res.json();
  }

  function renderMessages(messages) {
    list.replaceChildren();
    if (!messages.length) {
      const empty = document.createElement('p');
      empty.className = 'empty';
      empty.textContent = 'No messages yet. Ask a question to get started.';
      list.appendChild(empty);
      return;
    }
    for (const msg of messages) {
      const li = document.createElement('li');
      li.className = `msg msg-${msg.role}`;
      const role = document.createElement('span');
      role.className = 'role';
      role.textContent = msg.role === 'user' ? 'You' : 'AskDocs';
      const text = document.createElement('p');
      text.className = 'text';
      text.textContent = msg.text;
      li.appendChild(role);
      li.appendChild(text);
      list.appendChild(li);
    }
  }

  async function refresh() {
    status.textContent = 'Loading chat history…';
    try {
      const body = await api(`/messages?sessionId=${encodeURIComponent(sessionId())}`);
      renderMessages(body.messages || []);
      status.textContent = '';
    } catch (err) {
      status.textContent = `Failed to load history: ${err.message}`;
    }
  }

  form.addEventListener('submit', async (ev) => {
    ev.preventDefault();
    const text = input.value.trim();
    if (!text) return;
    status.textContent = 'Sending…';
    try {
      await api('/chat', {
        method: 'POST',
        body: JSON.stringify({ sessionId: sessionId(), text }),
      });
      input.value = '';
      await refresh();
    } catch (err) {
      status.textContent = `Chat failed: ${err.message}`;
    }
  });

  refresh();
})();

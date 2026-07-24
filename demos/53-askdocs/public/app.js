(() => {
  const API_BASE =
    window.ASKDOCS_API_BASE ||
    `${window.location.protocol}//api.askdocs.localhost${window.location.port ? `:${window.location.port}` : ''}`;

  const SESSION_KEY = 'askdocs.sessionId';
  const form = document.getElementById('chat-form');
  const input = document.getElementById('chat-input');
  const list = document.getElementById('message-list');
  const status = document.getElementById('chat-status');
  const uploadForm = document.getElementById('upload-form');
  const uploadStatus = document.getElementById('upload-status');
  const docList = document.getElementById('document-list');
  const titleInput = document.getElementById('doc-title');
  const fileInput = document.getElementById('doc-file');
  const textInput = document.getElementById('doc-text');

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
    if (options.body && !(options.body instanceof FormData) && !headers['Content-Type']) {
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
    if (!list) return;
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
      if (msg.role === 'assistant' && Array.isArray(msg.citations) && msg.citations.length) {
        const cites = document.createElement('ul');
        cites.className = 'citations';
        for (const c of msg.citations) {
          const item = document.createElement('li');
          const title = c.title || c.documentId || 'document';
          const ordinal = typeof c.ordinal === 'number' ? ` #${c.ordinal}` : '';
          item.textContent = `Source: ${title}${ordinal}`;
          cites.appendChild(item);
        }
        li.appendChild(cites);
      }
      list.appendChild(li);
    }
  }

  function renderDocuments(documents) {
    if (!docList) return;
    docList.replaceChildren();
    if (!documents.length) {
      const empty = document.createElement('p');
      empty.className = 'empty';
      empty.textContent = 'No documents yet. Upload a handbook to start ingest.';
      docList.appendChild(empty);
      return;
    }
    for (const doc of documents) {
      const li = document.createElement('li');
      li.className = 'doc';
      li.textContent = `${doc.title} — ${doc.status} (${doc.id.slice(0, 8)}…)`;
      docList.appendChild(li);
    }
  }

  async function refreshChat() {
    if (!status) return;
    status.textContent = 'Loading chat history…';
    try {
      const body = await api(`/messages?sessionId=${encodeURIComponent(sessionId())}`);
      renderMessages(body.messages || []);
      status.textContent = '';
    } catch (err) {
      status.textContent = `Failed to load history: ${err.message}`;
    }
  }

  async function refreshDocuments() {
    if (!uploadStatus) return;
    try {
      const body = await api('/documents');
      renderDocuments(body.documents || []);
    } catch (err) {
      uploadStatus.textContent = `Failed to list documents: ${err.message}`;
    }
  }

  if (form && input && list && status) {
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
        await refreshChat();
      } catch (err) {
        status.textContent = `Chat failed: ${err.message}`;
      }
    });
    refreshChat();
  }

  if (uploadForm && uploadStatus) {
    uploadForm.addEventListener('submit', async (ev) => {
      ev.preventDefault();
      uploadStatus.textContent = 'Uploading…';
      try {
        const title = (titleInput && titleInput.value.trim()) || '';
        const pasted = (textInput && textInput.value.trim()) || '';
        const file = fileInput && fileInput.files && fileInput.files[0];
        if (file) {
          const fd = new FormData();
          if (title) fd.append('title', title);
          fd.append('file', file, file.name);
          await api('/documents', { method: 'POST', body: fd });
        } else if (pasted) {
          await api('/documents', {
            method: 'POST',
            body: JSON.stringify({
              title: title || 'Pasted document',
              text: pasted,
              filename: 'pasted.txt',
            }),
          });
        } else {
          throw new Error('Choose a file or paste text');
        }
        if (textInput) textInput.value = '';
        if (fileInput) fileInput.value = '';
        uploadStatus.textContent = 'Uploaded — ingest running…';
        await refreshDocuments();
      } catch (err) {
        uploadStatus.textContent = `Upload failed: ${err.message}`;
      }
    });
    refreshDocuments();
    setInterval(refreshDocuments, 4000);
  }
})();

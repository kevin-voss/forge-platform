(() => {
  const API_BASE =
    window.SNAPNOTE_API_BASE ||
    `${window.location.protocol}//api.snapnote.localhost${window.location.port ? `:${window.location.port}` : ''}`;

  const form = document.getElementById('note-form');
  const titleInput = document.getElementById('note-title');
  const bodyInput = document.getElementById('note-body');
  const list = document.getElementById('note-list');
  const status = document.getElementById('note-status');

  if (!form || !titleInput || !bodyInput || !list || !status) return;

  async function api(path, options = {}) {
    const res = await fetch(`${API_BASE}${path}`, {
      headers: { 'Content-Type': 'application/json', ...(options.headers || {}) },
      ...options,
    });
    if (!res.ok) {
      const text = await res.text();
      throw new Error(text || `HTTP ${res.status}`);
    }
    if (res.status === 204) return null;
    return res.json();
  }

  function renderNotes(notes) {
    list.replaceChildren();
    if (!notes.length) {
      const empty = document.createElement('p');
      empty.className = 'empty';
      empty.textContent = 'No notes yet.';
      list.appendChild(empty);
      return;
    }
    for (const note of notes) {
      const li = document.createElement('li');

      const meta = document.createElement('div');
      meta.className = 'note-meta';

      const title = document.createElement('span');
      title.className = 'title';
      title.textContent = note.title;

      const body = document.createElement('p');
      body.className = 'body';
      body.textContent = note.body || '';

      meta.appendChild(title);
      if (note.body) meta.appendChild(body);

      const actions = document.createElement('div');
      actions.className = 'actions';

      const del = document.createElement('button');
      del.type = 'button';
      del.className = 'secondary';
      del.textContent = 'Delete';
      del.addEventListener('click', async () => {
        try {
          await api(`/notes/${note.id}`, { method: 'DELETE' });
          await refresh();
        } catch (err) {
          status.textContent = `Failed to delete note: ${err.message}`;
        }
      });

      actions.appendChild(del);
      li.appendChild(meta);
      li.appendChild(actions);
      list.appendChild(li);
    }
  }

  async function refresh() {
    status.textContent = 'Loading notes…';
    try {
      const notes = await api('/notes');
      renderNotes(notes);
      status.textContent = `${notes.length} note${notes.length === 1 ? '' : 's'}`;
    } catch (err) {
      status.textContent = `API unavailable: ${err.message}`;
      list.replaceChildren();
    }
  }

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    const title = titleInput.value.trim();
    if (!title) return;
    try {
      await api('/notes', {
        method: 'POST',
        body: JSON.stringify({ title, body: bodyInput.value }),
      });
      titleInput.value = '';
      bodyInput.value = '';
      await refresh();
    } catch (err) {
      status.textContent = `Failed to create note: ${err.message}`;
    }
  });

  refresh();
})();

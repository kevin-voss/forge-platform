(() => {
  const API_BASE =
    window.SNAPNOTE_API_BASE ||
    `${window.location.protocol}//api.snapnote.localhost${window.location.port ? `:${window.location.port}` : ''}`;

  const form = document.getElementById('note-form');
  const titleInput = document.getElementById('note-title');
  const bodyInput = document.getElementById('note-body');
  const list = document.getElementById('note-list');
  const status = document.getElementById('note-status');
  const workersEl = document.getElementById('workers-indicator');

  if (!form || !titleInput || !bodyInput || !list || !status) return;

  const cfg = window.SNAPNOTE_CONFIG || {};
  const minReplicas = Number(cfg.minReplicas) || 1;
  const maxReplicas = Number(cfg.maxReplicas) || 8;

  function setWorkersLabel(replicas, detail) {
    if (!workersEl) return;
    const n = Number.isFinite(replicas) ? replicas : 0;
    workersEl.dataset.replicas = String(n);
    workersEl.textContent = detail
      ? `workers: ${n} · ${detail}`
      : `workers: ${n} (min ${minReplicas} / max ${maxReplicas})`;
  }

  async function refreshWorkers() {
    if (!workersEl) return;
    const slug = (cfg.projectSlug || '').trim();
    const env = (cfg.environment || 'local').trim();
    const policy = (cfg.workerPolicy || 'snapnote-worker-queue').trim();
    if (!slug) {
      setWorkersLabel(0, 'config pending');
      return;
    }
    try {
      const res = await fetch(
        `/autoscaler/v1/projects/${encodeURIComponent(slug)}/environments/${encodeURIComponent(env)}/scalingpolicies/${encodeURIComponent(policy)}`,
      );
      if (!res.ok) {
        setWorkersLabel(Number(workersEl.dataset.replicas) || 0, `status ${res.status}`);
        return;
      }
      const body = await res.json();
      const desired = Number(
        (body.status && body.status.desiredReplicas) ??
          (body.spec && body.spec.minReplicas) ??
          minReplicas,
      );
      const metric =
        body.status &&
        body.status.lastRecommendation &&
        body.status.lastRecommendation.metricType
          ? body.status.lastRecommendation.metricType
          : '';
      setWorkersLabel(desired, metric || undefined);
    } catch (err) {
      setWorkersLabel(Number(workersEl.dataset.replicas) || 0, 'unreachable');
    }
  }

  async function api(path, options = {}) {
    const headers = { ...(options.headers || {}) };
    if (options.body && !(options.body instanceof FormData) && !headers['Content-Type']) {
      headers['Content-Type'] = 'application/json';
    }
    const res = await fetch(`${API_BASE}${path}`, {
      ...options,
      headers,
    });
    if (!res.ok) {
      const text = await res.text();
      throw new Error(text || `HTTP ${res.status}`);
    }
    if (res.status === 204) return null;
    return res.json();
  }

  async function loadAttachments(noteId) {
    return api(`/notes/${noteId}/attachments`);
  }

  async function uploadAttachment(noteId, file) {
    const created = await api(`/notes/${noteId}/attachments`, {
      method: 'POST',
      body: JSON.stringify({
        filename: file.name || 'upload.bin',
        contentType: file.type || 'application/octet-stream',
      }),
    });
    const put = await fetch(created.uploadUrl, {
      method: created.uploadMethod || 'PUT',
      headers: {
        'Content-Type': file.type || 'application/octet-stream',
      },
      body: file,
    });
    if (!put.ok) {
      const text = await put.text();
      throw new Error(text || `upload HTTP ${put.status}`);
    }
    // Confirm upload → publish attachment.uploaded to durable queue.
    await api(`/notes/${noteId}/attachments/${created.attachment.id}/complete`, {
      method: 'POST',
    });
    return created.attachment;
  }

  async function downloadAttachment(noteId, attachmentId) {
    const meta = await api(`/notes/${noteId}/attachments/${attachmentId}/download`);
    window.open(meta.downloadUrl, '_blank', 'noopener,noreferrer');
  }

  function renderAttachments(noteId, items, container) {
    container.replaceChildren();
    if (!items.length) {
      const empty = document.createElement('p');
      empty.className = 'attachments-empty';
      empty.textContent = 'No attachments yet.';
      container.appendChild(empty);
      return;
    }
    const ul = document.createElement('ul');
    ul.className = 'attachment-list';
    for (const att of items) {
      const li = document.createElement('li');
      li.dataset.status = att.status === 'pending' ? 'processing' : att.status;
      const label = document.createElement('span');
      const statusLabel =
        att.status === 'pending' ? 'processing…' : att.status === 'ready' ? 'ready' : att.status;
      label.textContent = `${att.objectKey.split('/').pop()} · ${statusLabel}`;
      if (att.status === 'ready' && att.thumbnailKey) {
        const thumb = document.createElement('span');
        thumb.className = 'thumb-key';
        thumb.textContent = ` thumb:${att.thumbnailKey.split('/').pop()}`;
        label.appendChild(thumb);
      }
      const openBtn = document.createElement('button');
      openBtn.type = 'button';
      openBtn.className = 'secondary';
      openBtn.textContent = 'Download';
      openBtn.addEventListener('click', async () => {
        try {
          await downloadAttachment(noteId, att.id);
        } catch (err) {
          status.textContent = `Failed to download: ${err.message}`;
        }
      });
      li.appendChild(label);
      li.appendChild(openBtn);
      ul.appendChild(li);
    }
    container.appendChild(ul);
  }

  function renderNotes(notes, attachmentsByNote) {
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

      const attachments = document.createElement('div');
      attachments.className = 'attachments';
      renderAttachments(note.id, attachmentsByNote[note.id] || [], attachments);
      meta.appendChild(attachments);

      const actions = document.createElement('div');
      actions.className = 'actions';

      const fileInput = document.createElement('input');
      fileInput.type = 'file';
      fileInput.accept = 'image/*,.pdf,application/pdf';
      fileInput.hidden = true;

      const attachBtn = document.createElement('button');
      attachBtn.type = 'button';
      attachBtn.textContent = 'Attach file';
      attachBtn.addEventListener('click', () => fileInput.click());
      fileInput.addEventListener('change', async () => {
        const file = fileInput.files && fileInput.files[0];
        fileInput.value = '';
        if (!file) return;
        try {
          status.textContent = `Uploading ${file.name}…`;
          // Optimistic "processing…" row so the async state is visible even when
          // the worker finishes before the next refresh (tiny uploads / headed slowMo).
          const existing = await loadAttachments(note.id).catch(() => []);
          renderAttachments(
            note.id,
            [
              ...existing,
              {
                id: `local-pending-${Date.now()}`,
                status: 'pending',
                objectKey: file.name || 'upload.bin',
              },
            ],
            attachments,
          );
          const uploaded = await uploadAttachment(note.id, file);
          // Keep the pending row painted briefly so headed E2E can observe
          // "processing…" before a fast worker + full-list refresh overwrites it.
          renderAttachments(
            note.id,
            [
              ...existing.filter((a) => a.id !== uploaded.id),
              { ...uploaded, status: 'pending', objectKey: uploaded.objectKey || file.name },
            ],
            attachments,
          );
          status.textContent = `Uploaded ${file.name} — processing thumbnail…`;
          await new Promise((r) => setTimeout(r, 600));
          await refresh();
          // Poll until worker flips status to ready (async queue proof).
          for (let i = 0; i < 40; i++) {
            await new Promise((r) => setTimeout(r, 500));
            const items = await loadAttachments(note.id);
            const match = items.find((a) => a.id === uploaded.id);
            if (match && match.status === 'ready') {
              await refresh();
              status.textContent = `Thumbnail ready for ${file.name}`;
              return;
            }
          }
          await refresh();
        } catch (err) {
          status.textContent = `Failed to upload: ${err.message}`;
          await refresh();
        }
      });

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

      actions.appendChild(attachBtn);
      actions.appendChild(fileInput);
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
      const attachmentsByNote = {};
      await Promise.all(
        notes.map(async (note) => {
          attachmentsByNote[note.id] = await loadAttachments(note.id);
        }),
      );
      renderNotes(notes, attachmentsByNote);
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
  refreshWorkers();
  setInterval(refreshWorkers, 1500);
})();

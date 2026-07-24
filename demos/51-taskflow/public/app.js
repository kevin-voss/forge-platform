(() => {
  const API_BASE =
    window.TASKFLOW_API_BASE ||
    `${window.location.protocol}//api.taskflow.localhost${window.location.port ? `:${window.location.port}` : ''}`;

  const TOKEN_KEY = 'taskflow.token';
  const USER_KEY = 'taskflow.user';

  const authForm = document.getElementById('auth-form');
  const authEmail = document.getElementById('auth-email');
  const authPassword = document.getElementById('auth-password');
  const authSignup = document.getElementById('auth-signup');
  const authLogout = document.getElementById('auth-logout');
  const authStatus = document.getElementById('auth-status');
  const sessionBar = document.getElementById('session-bar');
  const sessionLabel = document.getElementById('session-label');
  const board = document.getElementById('board');
  const form = document.getElementById('task-form');
  const titleInput = document.getElementById('task-title');
  const list = document.getElementById('task-list');
  const status = document.getElementById('task-status');
  const deleteProjectBtn = document.getElementById('delete-project');

  if (!authForm || !form || !titleInput || !list || !status) return;

  function getToken() {
    return localStorage.getItem(TOKEN_KEY) || '';
  }

  function getUser() {
    try {
      return JSON.parse(localStorage.getItem(USER_KEY) || 'null');
    } catch {
      return null;
    }
  }

  function setSession(token, user) {
    localStorage.setItem(TOKEN_KEY, token);
    localStorage.setItem(USER_KEY, JSON.stringify(user));
  }

  function clearSession() {
    localStorage.removeItem(TOKEN_KEY);
    localStorage.removeItem(USER_KEY);
  }

  async function api(path, options = {}) {
    const headers = { 'Content-Type': 'application/json', ...(options.headers || {}) };
    const token = getToken();
    if (token) headers.Authorization = `Bearer ${token}`;
    const res = await fetch(`${API_BASE}${path}`, { ...options, headers });
    if (!res.ok) {
      const text = await res.text();
      throw new Error(text || `HTTP ${res.status}`);
    }
    if (res.status === 204) return null;
    return res.json();
  }

  function renderSession() {
    const user = getUser();
    const token = getToken();
    if (!token || !user) {
      sessionBar.hidden = true;
      board.hidden = true;
      authForm.hidden = false;
      deleteProjectBtn.hidden = true;
      return;
    }
    authForm.hidden = true;
    sessionBar.hidden = false;
    board.hidden = false;
    sessionLabel.textContent = `Signed in as ${user.email} (${user.role})`;
    deleteProjectBtn.hidden = user.role !== 'admin';
  }

  function renderTasks(tasks) {
    list.replaceChildren();
    if (!tasks.length) {
      const empty = document.createElement('p');
      empty.className = 'empty';
      empty.textContent = 'No tasks yet.';
      list.appendChild(empty);
      return;
    }
    for (const task of tasks) {
      const li = document.createElement('li');
      if (task.done) li.classList.add('done');

      const title = document.createElement('span');
      title.className = 'title';
      title.textContent = task.title;

      const actions = document.createElement('div');
      actions.className = 'actions';

      const toggle = document.createElement('button');
      toggle.type = 'button';
      toggle.className = 'secondary';
      toggle.textContent = task.done ? 'Reopen' : 'Complete';
      toggle.addEventListener('click', async () => {
        try {
          await api(`/tasks/${task.id}`, {
            method: 'PATCH',
            body: JSON.stringify({ done: !task.done }),
          });
          await refresh();
        } catch (err) {
          status.textContent = `Failed to update task: ${err.message}`;
        }
      });

      actions.appendChild(toggle);
      li.appendChild(title);
      li.appendChild(actions);
      list.appendChild(li);
    }
  }

  async function refresh() {
    if (!getToken()) {
      status.textContent = 'Sign in to load tasks.';
      list.replaceChildren();
      return;
    }
    status.textContent = 'Loading tasks…';
    try {
      const tasks = await api('/tasks');
      renderTasks(tasks);
      status.textContent = `${tasks.length} task${tasks.length === 1 ? '' : 's'}`;
    } catch (err) {
      status.textContent = `API unavailable: ${err.message}`;
      list.replaceChildren();
      if (String(err.message).includes('401') || String(err.message).includes('unauthenticated')) {
        clearSession();
        renderSession();
      }
    }
  }

  async function authenticate(path) {
    authStatus.textContent = 'Working…';
    const email = authEmail.value.trim();
    const password = authPassword.value;
    try {
      const body = await api(path, {
        method: 'POST',
        body: JSON.stringify({ email, password, displayName: email.split('@')[0] || email }),
      });
      const token = body.token || body.pat || body.jwt;
      if (!token || !body.user) throw new Error('auth response missing token/user');
      setSession(token, body.user);
      authStatus.textContent = '';
      authPassword.value = '';
      renderSession();
      await refresh();
    } catch (err) {
      authStatus.textContent = `Auth failed: ${err.message}`;
    }
  }

  authForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    await authenticate('/auth/login');
  });

  authSignup.addEventListener('click', async () => {
    await authenticate('/auth/signup');
  });

  authLogout.addEventListener('click', () => {
    clearSession();
    authStatus.textContent = 'Signed out.';
    renderSession();
    list.replaceChildren();
    status.textContent = '';
  });

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    const title = titleInput.value.trim();
    if (!title) return;
    try {
      await api('/tasks', { method: 'POST', body: JSON.stringify({ title }) });
      titleInput.value = '';
      await refresh();
    } catch (err) {
      status.textContent = `Failed to create task: ${err.message}`;
    }
  });

  deleteProjectBtn.addEventListener('click', async () => {
    if (!confirm('Delete the shared project? Tasks will be removed.')) return;
    try {
      await api('/projects/project-shared', { method: 'DELETE' });
      status.textContent = 'Project deleted.';
      await refresh();
    } catch (err) {
      status.textContent = `Delete project failed: ${err.message}`;
    }
  });

  renderSession();
  refresh();
})();

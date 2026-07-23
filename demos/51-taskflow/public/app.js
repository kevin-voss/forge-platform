(() => {
  const API_BASE =
    window.TASKFLOW_API_BASE ||
    `${window.location.protocol}//api.taskflow.localhost${window.location.port ? `:${window.location.port}` : ''}`;

  const form = document.getElementById('task-form');
  const titleInput = document.getElementById('task-title');
  const list = document.getElementById('task-list');
  const status = document.getElementById('task-status');

  if (!form || !titleInput || !list || !status) return;

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
    status.textContent = 'Loading tasks…';
    try {
      const tasks = await api('/tasks');
      renderTasks(tasks);
      status.textContent = `${tasks.length} task${tasks.length === 1 ? '' : 's'}`;
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
      await api('/tasks', { method: 'POST', body: JSON.stringify({ title }) });
      titleInput.value = '';
      await refresh();
    } catch (err) {
      status.textContent = `Failed to create task: ${err.message}`;
    }
  });

  refresh();
})();

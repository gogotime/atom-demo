// API client for Atoms-Lite

async function jsonFetch(url, opts = {}) {
  const res = await fetch(url, {
    credentials: 'include',
    headers: { 'Content-Type': 'application/json', ...(opts.headers || {}) },
    ...opts,
  });
  const ct = res.headers.get('content-type') || '';
  if (!res.ok) {
    let msg = `HTTP ${res.status}`;
    if (ct.includes('application/json')) {
      const j = await res.json().catch(() => ({}));
      msg = j.error || msg;
    }
    throw new Error(msg);
  }
  if (ct.includes('application/json')) {
    return res.json();
  }
  return res.text();
}

export const getMe = () => fetch('/api/me', { credentials: 'include' }).then(async (r) => {
  if (!r.ok) return null;
  const j = await r.json();
  return j.user || j;
});

export const register = (email, password) =>
  jsonFetch('/api/register', { method: 'POST', body: JSON.stringify({ email, password }) });

export const login = (email, password) =>
  jsonFetch('/api/login', { method: 'POST', body: JSON.stringify({ email, password }) });

export const logout = () => jsonFetch('/api/logout', { method: 'POST' });

export const listProjects = () => jsonFetch('/api/projects');
export const createProject = (name) =>
  jsonFetch('/api/projects', { method: 'POST', body: JSON.stringify({ name: name || '' }) });
export const getProject = (id) => jsonFetch(`/api/projects/${id}`);
export const getProjectCode = (id) => jsonFetch(`/api/projects/${id}/code`);
export const getStreamInfo = (id) => jsonFetch(`/api/projects/${id}/stream/info`);
export const updateProject = (id, patch) =>
  jsonFetch(`/api/projects/${id}`, { method: 'PATCH', body: JSON.stringify(patch) });
export const deleteProject = (id) =>
  jsonFetch(`/api/projects/${id}`, { method: 'DELETE' });
export const publishProject = (id, is_published) =>
  jsonFetch(`/api/projects/${id}/publish`, { method: 'POST', body: JSON.stringify({ is_published }) });

export const getPublicProject = (slug) =>
  jsonFetch(`/api/public/${slug}`);

// SSE streaming generator
export function generateProject(id, message, onEvent) {
  return new Promise((resolve, reject) => {
    fetch(`/api/projects/${id}/generate`, {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ message }),
    }).then(async (response) => {
      if (!response.ok) {
        const j = await response.json().catch(() => ({}));
        reject(new Error(j.error || `HTTP ${response.status}`));
        return;
      }
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';
      while (true) {
        const { value, done } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        let idx;
        while ((idx = buffer.indexOf('\n\n')) !== -1) {
          const chunk = buffer.slice(0, idx);
          buffer = buffer.slice(idx + 2);
          let eventName = 'message';
          const dataLines = [];
          for (const line of chunk.split('\n')) {
            if (line.startsWith('event:')) eventName = line.slice(6).trim();
            else if (line.startsWith('data:')) dataLines.push(line.slice(5).trim());
          }
          if (dataLines.length === 0) continue;
          const dataStr = dataLines.join('\n');
          let env = {};
          try { env = JSON.parse(dataStr); } catch { env = { d: { raw: dataStr } }; }
          const evt = { type: env.t || eventName, data: env.d, i: env.i };
          if (evt.type === 'error') {
            const err = new Error(evt.data?.error || 'unknown error');
            if (evt.data?.detail) console.error('LLM error:', evt.data.detail);
            onEvent && onEvent({ ...evt, error: err });
            reject(err);
            return;
          }
          onEvent && onEvent(evt);
          if (evt.type === 'done') {
            resolve();
            return;
          }
        }
      }
      resolve();
    }).catch(reject);
  });
}

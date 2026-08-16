import { useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { listProjects, createProject, deleteProject, logout } from '../api.js';

// Parse the SQLite "YYYY-MM-DD HH:MM:SS" UTC format produced by default
// `sql.Open("sqlite", ...)` + `datetime('now')`. Always interpreted as UTC
// regardless of the browser's local timezone.
function parseServerTime(s) {
  if (!s) return 0;
  // SQLite default format: "YYYY-MM-DD HH:MM:SS" (no timezone, treat as UTC)
  if (s.includes('T')) return new Date(s).getTime();
  return new Date(s.replace(' ', 'T') + 'Z').getTime();
}

function timeAgo(s) {
  const t = parseServerTime(s);
  if (!t) return '';
  const d = Date.now() - t;
  if (d < 0) return 'just now';
  const m = Math.floor(d / 60000);
  if (m < 1) return 'just now';
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const dy = Math.floor(h / 24);
  if (dy < 30) return `${dy}d ago`;
  return s.slice(0, 10);
}

export default function Dashboard({ user, setUser }) {
  const navigate = useNavigate();
  const [projects, setProjects] = useState(null);
  const [err, setErr] = useState('');
  const [creating, setCreating] = useState(false);

  const load = () => {
    listProjects().then((r) => setProjects(r.projects || [])).catch((e) => setErr(e.message));
  };
  useEffect(load, []);

  const newProject = async () => {
    setCreating(true);
    try {
      const r = await createProject('Untitled Project');
      navigate(`/project/${r.id}`);
    } catch (e) {
      setErr(e.message);
    } finally {
      setCreating(false);
    }
  };

  const onDelete = async (id, e) => {
    e.preventDefault();
    e.stopPropagation();
    // Per requirements: direct delete, no confirmation.
    await deleteProject(id);
    load();
  };

  const handleLogout = async () => {
    await logout();
    setUser(null);
  };

  return (
    <div className="min-h-screen">
      <header className="px-6 md:px-12 py-5 flex items-center justify-between border-b border-slate-800/60">
        <Link to="/" className="flex items-center gap-2">
          <div className="w-8 h-8 rounded-full bg-gradient-to-br from-purple-500 to-cyan-400" />
          <span className="font-semibold text-lg">Atoms-Lite</span>
        </Link>
        <div className="flex items-center gap-3 text-sm">
          <span className="text-slate-400 hidden sm:inline">{user?.email}</span>
          <button onClick={handleLogout} className="btn-ghost">Logout</button>
        </div>
      </header>

      <main className="px-6 md:px-12 py-10 max-w-6xl mx-auto">
        <div className="flex items-center justify-between mb-6">
          <div>
            <h1 className="text-2xl font-semibold">My Projects</h1>
            <p className="text-slate-400 text-sm mt-1">Build and iterate on apps with your AI team.</p>
          </div>
          <button onClick={newProject} disabled={creating} className="btn-primary">
            {creating ? 'Creating…' : '+ New Project'}
          </button>
        </div>

        {err && <div className="mb-4 p-3 rounded-lg bg-red-500/10 border border-red-500/30 text-red-300 text-sm">{err}</div>}

        {projects === null && <div className="text-slate-400">Loading…</div>}
        {projects && projects.length === 0 && (
          <div className="card p-16 text-center">
            <div className="text-5xl mb-4">🚀</div>
            <h2 className="text-xl font-semibold mb-2">No projects yet</h2>
            <p className="text-slate-400 mb-6">Create your first project and your AI team will get to work.</p>
            <button onClick={newProject} disabled={creating} className="btn-primary">
              + Create your first project
            </button>
          </div>
        )}

        {projects && projects.length > 0 && (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {projects.map((p) => (
              <Link key={p.id} to={`/project/${p.id}`} className="card p-5 hover:border-purple-500/40 transition group">
                <div className="flex items-start justify-between mb-3">
                  <h3 className="font-semibold text-white truncate flex-1">{p.name}</h3>
                  {p.is_published && (
                    <span className="ml-2 text-xs px-2 py-0.5 rounded-full bg-cyan-500/15 text-cyan-300 border border-cyan-500/30">Published</span>
                  )}
                </div>
                <p className="text-sm text-slate-400 line-clamp-2 min-h-[2.5rem]">
                  {p.prompt ? p.prompt.slice(0, 100) : 'No prompt yet — open and start chatting.'}
                </p>
                <div className="mt-4 flex items-center justify-between text-xs text-slate-500">
                  <span>Updated {timeAgo(p.updated_at)}</span>
                  <button onClick={(e) => onDelete(p.id, e)} className="btn-danger opacity-0 group-hover:opacity-100 transition">
                    Delete
                  </button>
                </div>
              </Link>
            ))}
          </div>
        )}
      </main>
    </div>
  );
}

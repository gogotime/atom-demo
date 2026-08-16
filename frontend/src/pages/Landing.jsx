import { Link, useNavigate } from 'react-router-dom';
import { logout } from '../api.js';

const AGENTS = [
  { emoji: '🎯', name: 'Mike', role: 'Team Leader', desc: 'Coordinates the whole team. Breaks your idea into clear, actionable steps.' },
  { emoji: '📋', name: 'Emma', role: 'Product Manager', desc: 'Sharpens the spec. Locks down MVP features and user flows.' },
  { emoji: '🏗️', name: 'Bob', role: 'Architect', desc: 'Picks the stack. Designs the page structure and components.' },
  { emoji: '⚡', name: 'Alex', role: 'Engineer', desc: 'Writes the code. Ships a working web app you can preview live.' },
];

export default function Landing({ user, setUser }) {
  const navigate = useNavigate();
  const cta = () => {
    if (user) navigate('/dashboard');
    else navigate('/register');
  };
  const handleLogout = async () => {
    await logout();
    setUser(null);
  };

  return (
    <div className="min-h-screen">
      {/* Nav */}
      <header className="px-6 md:px-12 py-5 flex items-center justify-between border-b border-slate-800/60">
        <Link to="/" className="flex items-center gap-2">
          <div className="w-8 h-8 rounded-full bg-gradient-to-br from-purple-500 to-cyan-400" />
          <span className="font-semibold text-lg">Atoms-Lite</span>
        </Link>
        <nav className="flex items-center gap-3">
          {user ? (
            <>
              <Link to="/dashboard" className="text-slate-300 hover:text-white text-sm">Dashboard</Link>
              <span className="text-slate-500 text-sm">{user.email}</span>
              <button onClick={handleLogout} className="btn-ghost text-sm">Logout</button>
            </>
          ) : (
            <>
              <Link to="/login" className="text-slate-300 hover:text-white text-sm">Login</Link>
              <Link to="/register" className="btn-primary text-sm">Sign up</Link>
            </>
          )}
        </nav>
      </header>

      {/* Hero */}
      <section className="px-6 md:px-12 pt-20 pb-16 text-center">
        <div className="inline-block mb-4 px-3 py-1 rounded-full border border-purple-500/30 bg-purple-500/10 text-purple-300 text-xs font-medium">
          Multi-agent vibe coding, in your browser
        </div>
        <h1 className="text-4xl md:text-6xl font-bold tracking-tight mb-6 bg-gradient-to-r from-white via-purple-200 to-cyan-200 bg-clip-text text-transparent">
          Turn ideas into apps.<br />In minutes.
        </h1>
        <p className="text-slate-400 text-lg max-w-2xl mx-auto mb-10">
          Describe what you want. Watch 4 AI specialists build it together — then publish to a public link.
        </p>
        <button onClick={cta} className="btn-primary text-base px-7 py-3">
          {user ? 'Open Dashboard' : 'Start building →'}
        </button>
      </section>

      {/* Agents */}
      <section className="px-6 md:px-12 py-12 max-w-6xl mx-auto">
        <h2 className="text-center text-slate-300 text-sm uppercase tracking-widest mb-8">Meet your AI team</h2>
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
          {AGENTS.map((a) => (
            <div key={a.name} className="card p-5 hover:border-purple-500/40 transition">
              <div className="text-3xl mb-2">{a.emoji}</div>
              <div className="font-semibold text-white">{a.name}</div>
              <div className="text-xs text-purple-300 mb-2">{a.role}</div>
              <p className="text-sm text-slate-400 leading-relaxed">{a.desc}</p>
            </div>
          ))}
        </div>
      </section>

      {/* How it works */}
      <section className="px-6 md:px-12 py-12 max-w-5xl mx-auto">
        <h2 className="text-center text-slate-300 text-sm uppercase tracking-widest mb-8">How it works</h2>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          {[
            { n: '01', t: 'Describe', d: 'Type your idea in plain English. "A pomodoro timer with stats" is enough.' },
            { n: '02', t: 'Collaborate', d: 'Watch the 4 agents discuss, plan, and build — in real time.' },
            { n: '03', t: 'Publish', d: 'Iterate by chatting. Hit publish to share a public link.' },
          ].map((s) => (
            <div key={s.n} className="card p-6">
              <div className="text-purple-400 font-mono text-sm mb-2">{s.n}</div>
              <div className="font-semibold text-white text-lg mb-2">{s.t}</div>
              <p className="text-sm text-slate-400">{s.d}</p>
            </div>
          ))}
        </div>
      </section>

      <footer className="px-6 md:px-12 py-8 border-t border-slate-800/60 text-center text-slate-500 text-sm">
        Built with Go + React. Powered by your OpenAI-compatible API.
      </footer>
    </div>
  );
}

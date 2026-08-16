import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { register } from '../api.js';

export default function Register({ setUser }) {
  const navigate = useNavigate();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [err, setErr] = useState('');
  const [loading, setLoading] = useState(false);

  const submit = async (e) => {
    e.preventDefault();
    setErr('');
    setLoading(true);
    try {
      const r = await register(email, password);
      setUser(r.user);
      navigate('/dashboard');
    } catch (e) {
      setErr(e.message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen flex items-center justify-center px-4">
      <div className="w-full max-w-md">
        <Link to="/" className="flex items-center justify-center gap-2 mb-8">
          <div className="w-9 h-9 rounded-full bg-gradient-to-br from-purple-500 to-cyan-400" />
          <span className="font-semibold text-lg">Atoms-Lite</span>
        </Link>
        <div className="card p-8">
          <h1 className="text-2xl font-semibold mb-1">Create your account</h1>
          <p className="text-slate-400 text-sm mb-6">Start building with your AI team.</p>
          {err && <div className="mb-4 p-3 rounded-lg bg-red-500/10 border border-red-500/30 text-red-300 text-sm">{err}</div>}
          <form onSubmit={submit} className="space-y-4">
            <div>
              <label className="block text-xs text-slate-400 mb-1.5">Email</label>
              <input className="input" type="email" required value={email} onChange={(e) => setEmail(e.target.value)} placeholder="you@example.com" />
            </div>
            <div>
              <label className="block text-xs text-slate-400 mb-1.5">Password</label>
              <input className="input" type="password" required minLength={6} value={password} onChange={(e) => setPassword(e.target.value)} placeholder="At least 6 characters" />
            </div>
            <button type="submit" disabled={loading} className="btn-primary w-full disabled:opacity-50">
              {loading ? 'Creating…' : 'Create account'}
            </button>
          </form>
          <div className="mt-6 text-center text-sm text-slate-400">
            Already have an account? <Link to="/login" className="text-purple-300 hover:text-purple-200">Log in</Link>
          </div>
        </div>
      </div>
    </div>
  );
}

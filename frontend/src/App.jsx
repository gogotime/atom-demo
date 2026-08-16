import { Routes, Route, Navigate } from 'react-router-dom';
import { useEffect, useState } from 'react';
import Landing from './pages/Landing.jsx';
import Login from './pages/Login.jsx';
import Register from './pages/Register.jsx';
import Dashboard from './pages/Dashboard.jsx';
import Builder from './pages/Builder.jsx';
import Public from './pages/Public.jsx';
import { getMe } from './api.js';

function Protected({ children, user, setUser }) {
  if (user === null) return <Navigate to="/login" replace />;
  if (user === undefined) return <div className="min-h-screen flex items-center justify-center text-slate-400">Loading…</div>;
  return children;
}

export default function App() {
  const [user, setUser] = useState(undefined); // undefined = loading, null = guest

  useEffect(() => {
    getMe().then((u) => setUser(u)).catch(() => setUser(null));
  }, []);

  return (
    <Routes>
      <Route path="/" element={<Landing user={user} setUser={setUser} />} />
      <Route path="/login" element={<Login setUser={setUser} />} />
      <Route path="/register" element={<Register setUser={setUser} />} />
      <Route path="/dashboard" element={<Protected user={user}><Dashboard user={user} setUser={setUser} /></Protected>} />
      <Route path="/project/:id" element={<Protected user={user}><Builder user={user} setUser={setUser} /></Protected>} />
      <Route path="/p/:slug" element={<Public />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}

import { useEffect, useRef, useState, useCallback } from 'react';
import { Link, useParams } from 'react-router-dom';
import { getProject, getProjectCode, updateProject, publishProject, generateProject } from '../api.js';
import ChatPanel from '../components/ChatPanel.jsx';
import Preview from '../components/Preview.jsx';

export default function Builder({ user }) {
  const { id } = useParams();
  const [project, setProject] = useState(null);
  const [messages, setMessages] = useState([]);
  const [code, setCode] = useState('');
  const [generating, setGenerating] = useState(false);
  const [toolActive, setToolActive] = useState(false);
  const [toolName, setToolName] = useState('');
  const [err, setErr] = useState('');
  const [nameEditing, setNameEditing] = useState(false);
  const [nameDraft, setNameDraft] = useState('');
  const [publishedLink, setPublishedLink] = useState(null);
  const codeRef = useRef('');
  const generatingRef = useRef(false);
  const toolNameRef = useRef('');
  const activeToolsRef = useRef({});
  const streamingRef = useRef(null);
  const esRef = useRef(null);
  const lastSeenRef = useRef(0);
  const textFilterRef = useRef({ state: 'open', buffer: '' });

  const load = useCallback(async () => {
    try {
      const [proj, codeResp] = await Promise.all([
        getProject(id),
        getProjectCode(id).catch(() => ({ html: '' })),
      ]);
      setProject(proj.project);
      setMessages(proj.messages || []);
      setNameDraft(proj.project.name);
      setCode(codeResp.html || '');
      codeRef.current = codeResp.html || '';
      lastSeenRef.current = Number(proj.project.stream_offset) || 0;
      if (proj.project.is_published && proj.project.slug) {
        setPublishedLink(`${window.location.origin}/p/${proj.project.slug}`);
      } else {
        setPublishedLink(null);
      }
      setGenerating(!!proj.project.is_generating);
    } catch (e) {
      setErr(e.message);
    }
  }, [id]);

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    if (!project) return;
    if (esRef.current) {
      esRef.current.close();
      esRef.current = null;
    }
    if (!project.is_generating && lastSeenRef.current >= Number(project.stream_offset)) {
      return;
    }
    const es = new EventSource(`/api/projects/${id}/stream?from=${lastSeenRef.current}`, { withCredentials: true });
    esRef.current = es;
    es.onmessage = (e) => {
      try {
        const env = JSON.parse(e.data);
        if (typeof env.i === 'number') {
          lastSeenRef.current = env.i;
        }
        applyStreamEvent({ type: env.t, data: env.d, i: env.i });
      } catch {}
    };
    es.onerror = () => {
      es.close();
      esRef.current = null;
      setGenerating(false);
    };
    return () => {
      es.close();
      esRef.current = null;
    };
  }, [project?.is_generating, project?.stream_offset, id]);

  function applyStreamEvent(env) {
    const t = env.type;
    const d = env.data || {};
    if (typeof env.i === 'number') {
      lastSeenRef.current = env.i;
    }
    if (t === 'text') {
      // Defensive filter: backend may have leaked a "[tool_call: ...] {...}" artifact
      // emitted by the model as plain text. Buffer tentatively to confirm, then drop.
      const incoming = d.text || '';
      const f = textFilterRef.current;
      const flushTentative = (text) => {
        if (!text) return;
        setMessages((m) => {
          const idx = streamingRef.current;
          if (idx == null || idx >= m.length) return m;
          const updated = [...m];
          updated[idx] = { ...updated[idx], content: (updated[idx].content || '') + text };
          return updated;
        });
      };
      if (f.state === 'open') {
        if (incoming.startsWith('[')) {
          f.state = 'tentative';
          f.buffer = incoming;
          return;
        }
        flushTentative(incoming);
      } else if (f.state === 'tentative') {
        f.buffer += incoming;
        const buf = f.buffer;
        if (buf.startsWith('[tool_call:')) {
          f.state = 'suppressed';
        } else if (!buf.startsWith('[tool_call:')) {
          f.state = 'open';
          flushTentative(buf);
          f.buffer = '';
        }
      } else {
        // suppressed — drop until next turn resets the filter
      }
    } else if (t === 'tool_call') {
      setToolActive(true);
      const tn = d.name || '';
      setToolName(tn);
      toolNameRef.current = tn;
      if (d.id) activeToolsRef.current[d.id] = tn;
    } else if (t === 'tool_result') {
      const tn = (d.id && activeToolsRef.current[d.id]) || toolNameRef.current;
      if (d.id) delete activeToolsRef.current[d.id];
      const modifiesCode = (tn === 'update_files' || tn === 'patch_files');
      setToolActive(false);
      setToolName('');
      toolNameRef.current = '';
      if (modifiesCode) {
        setMessages((m) => {
          const updated = [...m];
          updated.push({
            role: 'tool',
            tool_name: tn,
            content: tn || 'tool applied',
            created_at: new Date().toISOString(),
          });
          return updated;
        });
      }
      if (!d.is_error && modifiesCode) {
        getProjectCode(id).then((c) => {
          setCode(c.html || '');
          codeRef.current = c.html || '';
        }).catch(() => {});
      }
    } else if (t === 'error') {
      setErr(d.error || 'generation failed');
    } else if (t === 'done') {
      streamingRef.current = null;
      setGenerating(false);
      setToolActive(false);
    }
  }

  const send = async (text) => {
    if (generatingRef.current) return;
    generatingRef.current = true;
    setGenerating(true);
    setErr('');
    setToolActive(false);
    setToolName('');
    toolNameRef.current = '';

    const userMsg = { role: 'user', content: text, created_at: new Date().toISOString() };
    const assistantMsg = { role: 'assistant', content: '', created_at: new Date().toISOString() };
    textFilterRef.current = { state: 'open', buffer: '' };
    setMessages((m) => {
      const next = [...m, userMsg, assistantMsg];
      streamingRef.current = next.length - 1;
      return next;
    });

    try {
      await generateProject(id, text, applyStreamEvent);
    } catch (e) {
      setErr(e.message);
    } finally {
      setGenerating(false);
      setToolActive(false);
      streamingRef.current = null;
      generatingRef.current = false;
      load();
    }
  };

  const saveName = async () => {
    setNameEditing(false);
    const newName = nameDraft.trim() || 'Untitled Project';
    if (newName === project?.name) return;
    try {
      const r = await updateProject(id, { name: newName });
      setProject(r.project);
    } catch (e) {
      setErr(e.message);
    }
  };

  const togglePublish = async () => {
    if (!project) return;
    const newPub = !project.is_published;
    try {
      const r = await publishProject(id, newPub);
      if (newPub && r.slug) {
        setPublishedLink(`${window.location.origin}/p/${r.slug}`);
      } else {
        setPublishedLink(null);
      }
      load();
    } catch (e) {
      setErr(e.message);
    }
  };

  if (!project) {
    return <div className="min-h-screen flex items-center justify-center text-slate-400">Loading project…</div>;
  }

  const copyLink = async () => {
    if (!publishedLink) return;
    try { await navigator.clipboard.writeText(publishedLink); } catch {}
  };

  return (
    <div className="h-screen flex flex-col">
      <header className="flex items-center justify-between px-4 md:px-6 py-3 border-b border-slate-800 bg-bg-soft/80 backdrop-blur">
        <div className="flex items-center gap-3 min-w-0">
          <Link to="/dashboard" className="text-slate-400 hover:text-white text-sm">← Back</Link>
          {nameEditing ? (
            <input
              autoFocus
              maxLength={120}
              className="bg-transparent border-b border-purple-500 outline-none text-white font-medium px-1"
              value={nameDraft}
              onChange={(e) => setNameDraft(e.target.value)}
              onBlur={saveName}
              onKeyDown={(e) => { if (e.key === 'Enter') saveName(); if (e.key === 'Escape') { setNameEditing(false); setNameDraft(project.name); } }}
            />
          ) : (
            <button onClick={() => setNameEditing(true)} className="font-medium text-white truncate hover:text-purple-300">
              {project.name}
            </button>
          )}
          {project.is_published && (
            <span className="text-xs px-2 py-0.5 rounded-full bg-cyan-500/15 text-cyan-300 border border-cyan-500/30">Published</span>
          )}
          {generating && (
            <span className="text-xs px-2 py-0.5 rounded-full bg-purple-500/15 text-purple-300 border border-purple-500/30">Generating…</span>
          )}
        </div>
        <div className="flex items-center gap-2">
          {publishedLink && (
            <>
              <code className="hidden md:block text-xs text-slate-400 bg-bg-card px-2 py-1 rounded max-w-[180px] truncate">
                {publishedLink}
              </code>
              <button onClick={copyLink} className="btn-ghost text-xs">Copy link</button>
            </>
          )}
          <button onClick={togglePublish} className={project.is_published ? 'btn-ghost text-xs' : 'btn-primary text-xs'}>
            {project.is_published ? 'Unpublish' : 'Publish'}
          </button>
          <span className="hidden sm:inline text-xs text-slate-500 ml-2">{user?.email}</span>
        </div>
      </header>

      {err && (
        <div className="px-4 md:px-6 py-2 bg-red-500/10 border-b border-red-500/30 text-red-300 text-sm flex items-center justify-between">
          <span className="truncate">{err}</span>
          <button onClick={() => setErr('')} className="text-red-300 hover:text-white text-xs flex-shrink-0">Dismiss</button>
        </div>
      )}

      <div className="flex-1 flex flex-col md:flex-row overflow-hidden">
        <div className="md:w-[45%] md:min-w-[380px] md:max-w-[640px] border-r border-slate-800 flex flex-col h-1/2 md:h-full">
          <ChatPanel
            messages={messages}
            generating={generating}
            toolActive={toolActive}
            toolName={toolName}
            onSend={send}
          />
        </div>
        <div className="flex-1 h-1/2 md:h-full">
          <Preview html={code} />
        </div>
      </div>
    </div>
  );
}

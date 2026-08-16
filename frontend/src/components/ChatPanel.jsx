import { useEffect, useRef, useState } from 'react';
import { marked } from 'marked';
import DOMPurify from 'dompurify';

function fmt(t) {
  try {
    return new Date(t).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  } catch { return ''; }
}

function toolBubbleText(toolName) {
  switch (toolName) {
    case 'update_files':
    case 'patch_files':
      return `${toolName} applied → preview updated`;
    case 'read_files':
      return 'read code';
    default:
      return toolName ? `${toolName} applied` : 'tool applied';
  }
}

function Bubble({ role, content, time, toolName }) {
  const safe = DOMPurify.sanitize(marked.parse(content || ''));
  if (role === 'user') {
    return (
      <div className="flex justify-end">
        <div className="max-w-[80%] bg-purple-600/20 border border-purple-500/30 rounded-2xl rounded-tr-md px-4 py-2.5">
          <div className="text-sm text-white whitespace-pre-wrap">{content}</div>
          <div className="text-[10px] text-slate-500 mt-1 text-right">{fmt(time)}</div>
        </div>
      </div>
    );
  }
  if (role === 'tool') {
    return (
      <div className="flex items-center gap-2 px-2 text-xs text-slate-500">
        <span className="agent-pulse">●</span>
        <span>{toolBubbleText(toolName)}</span>
      </div>
    );
  }
  return (
    <div className="flex gap-3">
      <div className="flex-shrink-0 w-9 h-9 rounded-full bg-bg-card border border-slate-700 flex items-center justify-center text-lg">
        🤖
      </div>
      <div className="flex-1 min-w-0">
        <div className="flex items-baseline gap-2 mb-1">
          <span className="font-semibold text-white text-sm">Engineer</span>
          <span className="text-xs text-slate-400">Assistant</span>
        </div>
        <div className="md-body bg-slate-500/10 border border-slate-500/30 rounded-2xl rounded-tl-md px-4 py-2.5 max-w-full overflow-hidden">
          <div dangerouslySetInnerHTML={{ __html: safe }} />
        </div>
        {time && <div className="text-[10px] text-slate-500 mt-1 ml-1">{fmt(time)}</div>}
      </div>
    </div>
  );
}

export default function ChatPanel({ messages, generating, toolActive, toolName, onSend }) {
  const [input, setInput] = useState('');
  const scrollRef = useRef(null);
  const taRef = useRef(null);

  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [messages, generating]);

  useEffect(() => {
    if (taRef.current) {
      taRef.current.style.height = 'auto';
      taRef.current.style.height = Math.min(taRef.current.scrollHeight, 200) + 'px';
    }
  }, [input]);

  const submit = () => {
    const t = input.trim();
    if (!t || generating) return;
    setInput('');
    onSend(t);
  };

  return (
    <div className="flex flex-col h-full bg-bg/50">
      <div ref={scrollRef} className="flex-1 overflow-y-auto px-4 md:px-6 py-4 space-y-4">
        {messages.length === 0 && (
          <div className="text-center text-slate-500 text-sm mt-12">
            <div className="text-4xl mb-3">💡</div>
            <p className="mb-1">Start by describing what you want to build.</p>
            <p className="text-xs text-slate-600">e.g. "A pomodoro timer with task list and stats"</p>
          </div>
        )}
        {messages.map((m, i) => (
          <Bubble key={i} role={m.role} content={m.content} time={m.created_at} toolName={m.tool_name} />
        ))}
        {toolActive && (
          <div className="flex items-center gap-2 text-xs text-slate-500 px-2">
            <span className="agent-pulse">●</span>
            <span>Calling {toolName || 'tool'}…</span>
          </div>
        )}
        {generating && !toolActive && (
          <div className="flex items-center gap-2 text-xs text-slate-500 px-2">
            <span className="agent-pulse">●</span>
            <span>Thinking…</span>
          </div>
        )}
      </div>

      <div className="border-t border-slate-800 px-4 md:px-6 py-3 bg-bg-soft/50">
        <div className="flex items-end gap-2">
          <textarea
            ref={taRef}
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                submit();
              }
            }}
            placeholder={generating ? 'Generating…' : 'Describe what to build or change… (Shift+Enter for newline)'}
            rows={1}
            disabled={generating}
            className="input resize-none max-h-[200px] disabled:opacity-50"
          />
          <button onClick={submit} disabled={generating || !input.trim()} className="btn-primary h-[42px] disabled:opacity-50">
            {generating ? '…' : 'Send'}
          </button>
        </div>
      </div>
    </div>
  );
}
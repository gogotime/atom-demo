import { useEffect, useRef, useState } from 'react';

export default function Preview({ html }) {
  const [view, setView] = useState('desktop');
  const [reloadKey, setReloadKey] = useState(0);
  const iframeRef = useRef(null);

  const srcdoc = html || '<!doctype html><html><body style="font-family:system-ui;padding:2rem;color:#666">No output yet — send a message to get started.</body></html>';

  const openInNewTab = () => {
    const blob = new Blob([srcdoc], { type: 'text/html' });
    const url = URL.createObjectURL(blob);
    const win = window.open(url, '_blank');
    setTimeout(() => URL.revokeObjectURL(url), 60_000);
    if (!win) URL.revokeObjectURL(url);
  };

  return (
    <div className="h-full flex flex-col bg-bg-soft/30">
      <div className="flex items-center justify-between px-4 py-2 border-b border-slate-800 bg-bg-soft/50">
        <div className="flex items-center gap-2 text-xs">
          <button
            onClick={() => setView('desktop')}
            className={`px-2.5 py-1 rounded ${view === 'desktop' ? 'bg-purple-600/30 text-purple-200' : 'text-slate-400 hover:text-white'}`}
          >
            Desktop
          </button>
          <button
            onClick={() => setView('mobile')}
            className={`px-2.5 py-1 rounded ${view === 'mobile' ? 'bg-purple-600/30 text-purple-200' : 'text-slate-400 hover:text-white'}`}
          >
            Mobile
          </button>
        </div>
        <div className="flex items-center gap-2">
          <button onClick={() => setReloadKey((k) => k + 1)} className="btn-ghost text-xs">↻ Refresh</button>
          <button onClick={openInNewTab} className="btn-ghost text-xs">↗ Open in new tab</button>
        </div>
      </div>

      <div className="flex-1 flex items-center justify-center p-4 overflow-auto">
        <div
          className="bg-white rounded-lg shadow-2xl overflow-hidden border border-slate-700"
          style={{
            width: view === 'mobile' ? '375px' : '100%',
            maxWidth: '100%',
            height: view === 'mobile' ? '667px' : '100%',
            maxHeight: '100%',
            transition: 'width 0.2s, height 0.2s',
          }}
        >
          <iframe
            key={reloadKey}
            ref={iframeRef}
            title="preview"
            srcDoc={srcdoc}
            sandbox="allow-scripts"
            className="w-full h-full block"
            style={{ border: 0, background: 'white' }}
          />
        </div>
      </div>
    </div>
  );
}

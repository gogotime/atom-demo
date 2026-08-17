import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { getPublicProject } from '../api.js';

const CSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: https:; font-src data:; connect-src 'none'; frame-ancestors 'self'; base-uri 'none'";

function withCsp(html) {
  if (!html) return '';
  const cspMeta = `<meta http-equiv="Content-Security-Policy" content="${CSP}">`;
  if (/<head[^>]*>/i.test(html)) {
    if (!/http-equiv\s*=\s*["']Content-Security-Policy/i.test(html)) {
      return html.replace(/<head[^>]*>/i, (m) => m + cspMeta);
    }
    return html;
  }
  return `<!DOCTYPE html><html><head>${cspMeta}<meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1.0"></head><body>${html}</body></html>`;
}

export default function Public() {
  const { slug } = useParams();
  const [data, setData] = useState(null);
  const [err, setErr] = useState('');

  useEffect(() => {
    getPublicProject(slug).then((d) => setData(d)).catch((e) => setErr(e.message));
  }, [slug]);

  if (err) {
    return (
      <div className="min-h-screen flex flex-col items-center justify-center px-4 text-slate-300">
        <div className="text-5xl mb-4">😶</div>
        <h1 className="text-xl font-semibold mb-2">Project not available</h1>
        <p className="text-slate-500 text-sm mb-6">{err}</p>
        <Link to="/" className="btn-primary">Back home</Link>
      </div>
    );
  }

  if (!data) {
    return <div className="min-h-screen flex items-center justify-center text-slate-400">Loading…</div>;
  }

  const fullHtml = withCsp(data.html || '');

  return (
    <div className="min-h-screen flex flex-col">
      <header className="px-4 md:px-6 py-3 flex items-center justify-between border-b border-slate-800 bg-bg-soft">
        <Link to="/" className="flex items-center gap-2">
          <div className="w-7 h-7 rounded-full bg-gradient-to-br from-purple-500 to-cyan-400" />
          <span className="text-sm font-medium text-slate-300">Atoms-Lite</span>
        </Link>
        <div className="text-xs text-slate-500">Made with Atoms-Lite</div>
      </header>
      <div className="flex-1 bg-white">
        <iframe
          title={data.name ? String(data.name).slice(0, 200) : 'Published app'}
          srcDoc={fullHtml}
          sandbox="allow-scripts allow-forms allow-modals"
          referrerPolicy="no-referrer"
          loading="lazy"
          className="w-full h-full block"
          style={{ border: 0, minHeight: 'calc(100vh - 56px)' }}
        />
      </div>
    </div>
  );
}
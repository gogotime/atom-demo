package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
)

//go:embed all:dist
var distFS embed.FS

func main() {
	addr := flag.String("addr", "", "listen address (overrides LISTEN_ADDR)")
	flag.Parse()

	// Load .env if present (silently ignore if missing)
	if err := godotenv.Load(); err != nil {
		log.Printf("no .env file loaded: %v", err)
	}

	if *addr != "" {
		os.Setenv("LISTEN_ADDR", *addr)
	}
	listen := os.Getenv("LISTEN_ADDR")
	if listen == "" {
		listen = ":8080"
	}

	// Fail-fast: require a JWT_SECRET. In dev, fall back to a generated one
	// so first-run works, but warn loudly. In prod this should always be set.
	if os.Getenv("JWT_SECRET") == "" {
		if os.Getenv("ENV") == "production" {
			log.Fatalf("JWT_SECRET is required in production; refusing to start")
		}
		devSecret := "dev-secret-" + randomHex(16)
		os.Setenv("JWT_SECRET", devSecret)
		log.Printf("WARN: JWT_SECRET not set; using ephemeral dev secret. Set JWT_SECRET in .env to persist sessions across restarts.")
	}
	if len(os.Getenv("JWT_SECRET")) < 16 {
		log.Fatalf("JWT_SECRET must be at least 16 characters; got %d", len(os.Getenv("JWT_SECRET")))
	}

	if err := initDB(); err != nil {
		log.Fatalf("initDB failed: %v", err)
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	// API routes
	r.Route("/api", func(r chi.Router) {
		r.Post("/register", handleRegister)
		r.Post("/login", handleLogin)
		r.Post("/logout", handleLogout)
		r.Get("/me", handleMe)

		// Public: serve published project data
		r.Get("/public/{slug}", handlePublicPage)

		// Authed
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)
			r.Get("/projects", handleListProjects)
			r.Post("/projects", handleCreateProject)
			r.Get("/projects/{id}", handleGetProject)
			r.Get("/projects/{id}/code", handleGetProjectCode)
			r.Get("/projects/{id}/stream/info", handleStreamInfo)
			r.Get("/projects/{id}/stream", handleStream)
			r.Patch("/projects/{id}", handleUpdateProject)
			r.Delete("/projects/{id}", handleDeleteProject)
			r.Post("/projects/{id}/generate", handleGenerate)
			r.Post("/projects/{id}/publish", handlePublishProject)
		})
	})

	// Health
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	// Serve embedded frontend (dist)
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// dev mode: dist may not exist; fall back to a placeholder index
		log.Printf("warn: no embedded dist: %v (dev mode — using fallback)", err)
		r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(fallbackIndex))
		})
	} else {
		fileServer := http.FileServer(http.FS(sub))
		r.Handle("/assets/*", fileServer)

		// Serve public page at /p/:slug — SPA route
		r.Get("/p/{slug}", spaIndexHandler(sub))

		// SPA fallback for all other unmatched routes
		r.Get("/*", spaIndexHandler(sub))
	}

	log.Printf("listening on %s", listen)
	if err := http.ListenAndServe(listen, r); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

func spaIndexHandler(f fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(f, "index.html")
		if err != nil {
			http.Error(w, "index.html missing", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (strings.HasPrefix(origin, "http://localhost:") || strings.HasPrefix(origin, "http://127.0.0.1:")) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

const fallbackIndex = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Atoms-Lite</title></head>
<body style="font-family: system-ui; padding: 2rem; background: #0a0a0f; color: #eee;">
<h1>Atoms-Lite Backend Running</h1>
<p>Frontend dist not embedded. Run the dev mode:</p>
<pre style="background:#222;padding:1rem;border-radius:8px;color:#7dd;">cd frontend &amp;&amp; npm install &amp;&amp; npm run dev</pre>
<p>Or rebuild for production:</p>
<pre style="background:#222;padding:1rem;border-radius:8px;color:#7dd;">cd frontend &amp;&amp; npm run build</pre>
</body></html>`

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

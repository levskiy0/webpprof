package webpprof

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const sessionCookieName = "webpprof_session"

const (
	loginFailureLimit  = 5
	loginFailureWindow = time.Minute
)

type loginFailure struct {
	count      int
	windowEnds time.Time
}

func (p *Profiler) register(router Router) {
	router.Handle(p.config.basePath, http.RedirectHandler(p.config.basePath+"/", http.StatusTemporaryRedirect))
	router.Handle(p.config.basePath+"/", http.StripPrefix(p.config.basePath, p.handler()))
}

func (p *Profiler) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", p.serveIndex)
	mux.HandleFunc("GET /app.js", p.serveAsset("ui/app.js"))
	mux.HandleFunc("GET /app.css", p.serveAsset("ui/app.css"))
	mux.HandleFunc("GET /theme.css", p.serveAsset("ui/theme.css"))
	mux.HandleFunc("GET /details.css", p.serveAsset("ui/details.css"))
	mux.HandleFunc("GET /logo.svg", p.serveAsset("ui/logo.svg"))
	mux.HandleFunc("POST /session", p.createSession)
	mux.HandleFunc("DELETE /session", p.deleteSession)
	mux.Handle("GET /api/events", p.authorize(http.HandlerFunc(p.listEvents)))
	mux.Handle("GET /api/events/{id}", p.authorize(http.HandlerFunc(p.getEvent)))
	mux.Handle("GET /api/requests/{id}/analysis", p.authorize(http.HandlerFunc(p.getRequestAnalysis)))
	mux.Handle("GET /api/stats", p.authorize(http.HandlerFunc(p.getStats)))
	mux.Handle("GET /api/runtime", p.authorize(http.HandlerFunc(p.getRuntimeStats)))
	mux.Handle("GET /api/queues", p.authorize(http.HandlerFunc(p.getQueueStats)))
	mux.Handle("GET /api/dashboard", p.authorize(http.HandlerFunc(p.getDashboard)))
	mux.Handle("DELETE /api/events", p.authorize(http.HandlerFunc(p.clearEvents)))
	mux.Handle("GET /ws", p.authorize(http.HandlerFunc(p.serveWebSocket)))
	return securityHeaders(mux)
}

func (p *Profiler) serveIndex(w http.ResponseWriter, _ *http.Request) {
	p.writeAsset(w, "ui/index.html", "text/html; charset=utf-8")
}

func (p *Profiler) serveAsset(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		p.writeAsset(w, name, mime.TypeByExtension(path.Ext(name)))
	}
}

func (p *Profiler) writeAsset(w http.ResponseWriter, name, contentType string) {
	data, err := fs.ReadFile(assets, name)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (p *Profiler) createSession(w http.ResponseWriter, r *http.Request) {
	if p.config.token == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	client := loginClient(r)
	if retryAfter, blocked := p.loginBlocked(client, time.Now()); blocked {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(time.Until(retryAfter).Seconds()))))
		writeError(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	defer r.Body.Close()
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	provided := sha256.Sum256([]byte(payload.Token))
	expected := sha256.Sum256([]byte(p.config.token))
	if subtle.ConstantTimeCompare(provided[:], expected[:]) != 1 {
		p.recordLoginFailure(client, time.Now())
		writeError(w, http.StatusUnauthorized, "invalid token")
		return
	}
	p.clearLoginFailures(client)
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: p.sessionToken, Path: p.config.basePath, HttpOnly: true, Secure: p.config.secureCookie, SameSite: http.SameSiteStrictMode, MaxAge: int((12 * time.Hour).Seconds())})
	w.WriteHeader(http.StatusNoContent)
}

func loginClient(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}

func (p *Profiler) loginBlocked(client string, now time.Time) (time.Time, bool) {
	p.loginMu.Lock()
	defer p.loginMu.Unlock()
	failure, ok := p.loginFailures[client]
	if !ok || !now.Before(failure.windowEnds) {
		delete(p.loginFailures, client)
		return time.Time{}, false
	}
	return failure.windowEnds, failure.count >= loginFailureLimit
}

func (p *Profiler) recordLoginFailure(client string, now time.Time) {
	p.loginMu.Lock()
	defer p.loginMu.Unlock()
	failure := p.loginFailures[client]
	if !now.Before(failure.windowEnds) {
		failure = loginFailure{windowEnds: now.Add(loginFailureWindow)}
	}
	failure.count++
	p.loginFailures[client] = failure
}

func (p *Profiler) clearLoginFailures(client string) {
	p.loginMu.Lock()
	delete(p.loginFailures, client)
	p.loginMu.Unlock()
}

func (p *Profiler) deleteSession(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Path: p.config.basePath, HttpOnly: true, Secure: p.config.secureCookie, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (p *Profiler) authorize(next http.Handler) http.Handler {
	if p.config.token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(p.sessionToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (p *Profiler) listEvents(w http.ResponseWriter, r *http.Request) {
	kind := Kind(r.URL.Query().Get("kind"))
	requestID := r.URL.Query().Get("request_id")
	tags := r.URL.Query()["tag"]
	after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	before, _ := strconv.ParseUint(r.URL.Query().Get("before"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1_000 {
		limit = 200
	}
	entries := p.store.listBefore(kind, requestID, tags, after, before, limit+1)
	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[1:]
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": entries, "has_more": hasMore, "stats": p.store.stats()})
}

func (p *Profiler) getEvent(w http.ResponseWriter, r *http.Request) {
	entry, ok := p.store.get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "event not found")
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

func (p *Profiler) getRequestAnalysis(w http.ResponseWriter, r *http.Request) {
	analysis, ok := p.AnalyzeRequest(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}
	writeJSON(w, http.StatusOK, analysis)
}

func (p *Profiler) getStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, p.store.stats())
}

func (p *Profiler) getRuntimeStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, p.RuntimeStats())
}

func (p *Profiler) getQueueStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, p.QueueStats(r.Context()))
}

func (p *Profiler) getDashboard(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, p.DashboardSnapshot(r.Context()))
}

func (p *Profiler) clearEvents(w http.ResponseWriter, _ *http.Request) {
	p.store.clear()
	w.WriteHeader(http.StatusNoContent)
}

func (p *Profiler) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if _, ok := p.config.allowedOrigins[origin]; ok {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self' ws: wss:; img-src 'self' data:; style-src 'self'; script-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

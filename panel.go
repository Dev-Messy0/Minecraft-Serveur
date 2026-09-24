package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Panel struct {
	cfg      *Config
	cfgPath  string
	mc       *MCServer
	tmpl     *template.Template
	sessions map[string]time.Time
	sessMu   sync.Mutex

	// Rate limiting login
	attempts map[string][]time.Time
	attMu    sync.Mutex
}

func NewPanel(cfg *Config, cfgPath string, mc *MCServer) (*Panel, error) {
	tmpl, err := template.ParseGlob("templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Panel{
		cfg:      cfg,
		cfgPath:  cfgPath,
		mc:       mc,
		tmpl:     tmpl,
		sessions: make(map[string]time.Time),
		attempts: make(map[string][]time.Time),
	}, nil
}

// -------------------- Sessions signées --------------------

func (p *Panel) sign(value string) string {
	mac := hmac.New(sha256.New, []byte(p.cfg.SessionSecret))
	mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (p *Panel) createSession() string {
	id := randomHex(24)
	p.sessMu.Lock()
	p.sessions[id] = time.Now().Add(8 * time.Hour)
	p.sessMu.Unlock()
	return id + "." + p.sign(id)
}

func (p *Panel) validSession(token string) bool {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return false
	}
	id, sig := parts[0], parts[1]
	if !hmac.Equal([]byte(sig), []byte(p.sign(id))) {
		return false
	}
	p.sessMu.Lock()
	defer p.sessMu.Unlock()
	exp, ok := p.sessions[id]
	if !ok || time.Now().After(exp) {
		delete(p.sessions, id)
		return false
	}
	return true
}

func (p *Panel) destroySession(token string) {
	id := strings.SplitN(token, ".", 2)[0]
	p.sessMu.Lock()
	delete(p.sessions, id)
	p.sessMu.Unlock()
}

// -------------------- Middleware --------------------

func (p *Panel) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("session")
		if err != nil || !p.validSession(c.Value) {
			http.Redirect(w, r, "/login?next="+r.URL.Path, http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (p *Panel) rateLimited(ip string) bool {
	p.attMu.Lock()
	defer p.attMu.Unlock()
	now := time.Now()
	cutoff := now.Add(-5 * time.Minute)
	tries := p.attempts[ip][:0]
	for _, t := range p.attempts[ip] {
		if t.After(cutoff) {
			tries = append(tries, t)
		}
	}
	p.attempts[ip] = tries
	return len(tries) >= 5
}

func (p *Panel) recordAttempt(ip string) {
	p.attMu.Lock()
	p.attempts[ip] = append(p.attempts[ip], time.Now())
	p.attMu.Unlock()
}

// -------------------- Handlers --------------------

func (p *Panel) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)

	if p.rateLimited(ip) {
		http.Error(w, "Trop de tentatives. Réessaie dans 5 min.", http.StatusTooManyRequests)
		return
	}

	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		user := r.FormValue("username")
		pwd := r.FormValue("password")

		if user == p.cfg.AdminUser && CheckPassword(p.cfg.PasswordHash, pwd) {
			token := p.createSession()
			http.SetCookie(w, &http.Cookie{
				Name:     "session",
				Value:    token,
				Path:     "/",
				HttpOnly: true,
				Secure:   true,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   8 * 3600,
			})
			next := r.URL.Query().Get("next")
			if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
				next = "/"
			}
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
		p.recordAttempt(ip)
		time.Sleep(500 * time.Millisecond)
		_ = p.tmpl.ExecuteTemplate(w, "login.html", map[string]any{"Error": "Identifiants invalides"})
		return
	}

	_ = p.tmpl.ExecuteTemplate(w, "login.html", nil)
}

func (p *Panel) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("session"); err == nil {
		p.destroySession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "session", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (p *Panel) handleDashboard(w http.ResponseWriter, r *http.Request) {
	_ = p.tmpl.ExecuteTemplate(w, "dashboard.html", map[string]any{
		"Running": p.mc.IsRunning(),
		"Port":    p.cfg.MCPort,
	})
}

func (p *Panel) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, p.mc.Snapshot())
}

func (p *Panel) handleStart(w http.ResponseWriter, r *http.Request) {
	err := p.mc.Start()
	writeJSON(w, map[string]any{"ok": err == nil, "running": p.mc.IsRunning(), "error": errStr(err)})
}

func (p *Panel) handleStop(w http.ResponseWriter, r *http.Request) {
	err := p.mc.Stop()
	writeJSON(w, map[string]any{"ok": err == nil, "running": p.mc.IsRunning(), "error": errStr(err)})
}

func (p *Panel) handleMOTD(w http.ResponseWriter, r *http.Request) {
	var body struct{ Motd string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "JSON invalide", http.StatusBadRequest)
		return
	}
	body.Motd = strings.TrimSpace(body.Motd)
	if body.Motd == "" || len(body.Motd) > 120 {
		http.Error(w, "MOTD invalide", http.StatusBadRequest)
		return
	}
	p.mc.SetMOTD(body.Motd)
	p.cfg.MCMOTD = body.Motd
	_ = SaveConfig(p.cfgPath, p.cfg)
	writeJSON(w, map[string]any{"ok": true, "motd": body.Motd})
}

func (p *Panel) handlePassword(w http.ResponseWriter, r *http.Request) {
	var body struct{ Old, New string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "JSON invalide", http.StatusBadRequest)
		return
	}
	if !CheckPassword(p.cfg.PasswordHash, body.Old) {
		http.Error(w, "Ancien mot de passe incorrect", http.StatusForbidden)
		return
	}
	if len(body.New) < 10 {
		http.Error(w, "Nouveau mot de passe trop court (min. 10)", http.StatusBadRequest)
		return
	}
	hash, _ := HashPassword(body.New)
	p.cfg.PasswordHash = hash
	_ = SaveConfig(p.cfgPath, p.cfg)
	writeJSON(w, map[string]any{"ok": true})
}

// -------------------- Utils --------------------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	return strings.Split(r.RemoteAddr, ":")[0]
}
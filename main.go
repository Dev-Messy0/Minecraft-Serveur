package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
	cfgPath := flag.String("config", "data/config.json", "chemin du fichier de config")
	addr := flag.String("addr", "127.0.0.1:8080", "adresse d'écoute du panel")
	tmplDir := flag.String("templates", "templates", "dossier des templates HTML")
	flag.Parse()

	// Le panel doit être lancé depuis le dossier contenant templates/
	if _, err := os.Stat(filepath.Join(*tmplDir, "login.html")); err != nil {
		log.Fatalf("templates introuvables dans %s", *tmplDir)
	}

	cfg, initialPwd, err := LoadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("config : %v", err)
	}
	if initialPwd != "" {
		fmt.Println("════════════════════════════════════════════════")
		fmt.Printf("  ⚠  Identifiants initiaux → %s / %s\n", cfg.AdminUser, initialPwd)
		fmt.Println("     Note-les puis change le mot de passe via le panel.")
		fmt.Println("════════════════════════════════════════════════")
	}

	mc := NewMCServer("0.0.0.0", cfg.MCPort)
	mc.SetMOTD(cfg.MCMOTD)

	panel, err := NewPanel(cfg, *cfgPath, mc)
	if err != nil {
		log.Fatalf("panel : %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/login", panel.handleLogin)
	mux.HandleFunc("/logout", panel.handleLogout)
	mux.HandleFunc("/", panel.requireAuth(panel.handleDashboard))
	mux.HandleFunc("/api/status", panel.requireAuth(panel.handleStatus))
	mux.HandleFunc("/api/start", panel.requireAuth(panel.handleStart))
	mux.HandleFunc("/api/stop", panel.requireAuth(panel.handleStop))
	mux.HandleFunc("/api/motd", panel.requireAuth(panel.handleMOTD))
	mux.HandleFunc("/api/password", panel.requireAuth(panel.handlePassword))

	log.Printf("Panel à l'écoute sur %s", *addr)
	if err := http.ListenAndServe(*addr, securityHeaders(mux)); err != nil {
		log.Fatal(err)
	}
}

// En-têtes de sécurité — Caddy ajoute aussi les siens mais ceinture + bretelles
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
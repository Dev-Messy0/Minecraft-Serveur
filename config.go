package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

type Config struct {
	AdminUser     string `json:"admin_user"`
	PasswordHash  string `json:"admin_password_hash"`
	MCPort        int    `json:"mc_port"`
	MCMOTD        string `json:"mc_motd"`
	SessionSecret string `json:"session_secret"`
}

// LoadConfig charge la config, dans cet ordre de priorité :
//   1. Fichier JSON existant (source de vérité)
//   2. Variables d'environnement (uniquement au 1er lancement)
//   3. Génération aléatoire (fallback)
//
// Renvoie (config, motDePasseEnClairSiGenere, erreur).
func LoadConfig(path string) (*Config, string, error) {
	envUser := strings.TrimSpace(os.Getenv("MCPANEL_ADMIN_USER"))
	envPwd := os.Getenv("MCPANEL_ADMIN_PASSWORD")
	envPort := os.Getenv("MCPANEL_MC_PORT")
	envMOTD := os.Getenv("MCPANEL_MOTD")

	// --- Cas 1 : fichier existant ---
	if _, err := os.Stat(path); err == nil {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, "", err
		}
		cfg := &Config{}
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, "", fmt.Errorf("config JSON invalide : %w", err)
		}

		// Régénère le secret s'il manque (migration)
		if cfg.SessionSecret == "" {
			cfg.SessionSecret = randomHex(32)
			_ = SaveConfig(path, cfg)
		}

		// Permet quand même d'écraser via env (utile pour rotation)
		changed := false
		if envUser != "" && envUser != cfg.AdminUser {
			cfg.AdminUser = envUser
			changed = true
		}
		if envPwd != "" {
			hash, err := bcrypt.GenerateFromPassword([]byte(envPwd), bcrypt.DefaultCost)
			if err != nil {
				return nil, "", err
			}
			cfg.PasswordHash = string(hash)
			changed = true
		}
		if envPort != "" {
			var p int
			if _, err := fmt.Sscanf(envPort, "%d", &p); err == nil && p > 0 && p < 65536 {
				cfg.MCPort = p
				changed = true
			}
		}
		if envMOTD != "" {
			cfg.MCMOTD = envMOTD
			changed = true
		}
		if changed {
			if err := SaveConfig(path, cfg); err != nil {
				return nil, "", err
			}
			fmt.Println("[config] mise à jour via variables d'environnement")
		}
		return cfg, "", nil
	}

	// --- Cas 2 : premier lancement ---
	cfg := &Config{
		AdminUser:     "admin",
		MCPort:        25565,
		MCMOTD:        "§aMon serveur Go",
		SessionSecret: randomHex(32),
	}

	// Écrase avec les env vars si présentes
	if envUser != "" {
		cfg.AdminUser = envUser
	}
	if envPort != "" {
		var p int
		if _, err := fmt.Sscanf(envPort, "%d", &p); err == nil && p > 0 && p < 65536 {
			cfg.MCPort = p
		}
	}
	if envMOTD != "" {
		cfg.MCMOTD = envMOTD
	}

	// Mot de passe : env > aléatoire
	plainPwd := ""
	if envPwd != "" {
		plainPwd = envPwd
	} else {
		plainPwd = randomString(16)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plainPwd), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", err
	}
	cfg.PasswordHash = string(hash)

	if err := SaveConfig(path, cfg); err != nil {
		return nil, "", err
	}
	_ = os.Chmod(path, 0o600)

	// Si le mot de passe vient d'une env var, on ne l'affiche pas (il est déjà connu)
	if envPwd != "" {
		return cfg, "", nil
	}
	return cfg, plainPwd, nil
}

func SaveConfig(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	// 0600 : seul le user mcpanel peut lire
	return os.WriteFile(path, data, 0o600)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func randomString(n int) string {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func HashPassword(pwd string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.DefaultCost)
	return string(h), err
}

func CheckPassword(hash, pwd string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pwd)) == nil
}

func (c *Config) String() string {
	return fmt.Sprintf("Config{user=%s, mc_port=%d}", c.AdminUser, c.MCPort)
}
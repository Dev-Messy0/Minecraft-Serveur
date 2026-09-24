package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/bcrypt"
)

type Config struct {
	AdminUser     string `json:"admin_user"`
	PasswordHash  string `json:"admin_password_hash"`
	MCPort        int    `json:"mc_port"`
	MCMOTD        string `json:"mc_motd"`
	SessionSecret string `json:"session_secret"`
}

func LoadConfig(path string) (*Config, string, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Premier lancement : mot de passe aléatoire
		pwd := randomString(16)
		hash, _ := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.DefaultCost)
		cfg := &Config{
			AdminUser:     "admin",
			PasswordHash:  string(hash),
			MCPort:        25565,
			MCMOTD:        "§aMon serveur Go",
			SessionSecret: randomHex(32),
		}
		if err := SaveConfig(path, cfg); err != nil {
			return nil, "", err
		}
		_ = os.Chmod(path, 0o600)
		return cfg, pwd, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	cfg := &Config{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, "", err
	}
	if cfg.SessionSecret == "" {
		cfg.SessionSecret = randomHex(32)
		_ = SaveConfig(path, cfg)
	}
	return cfg, "", nil
}

func SaveConfig(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
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
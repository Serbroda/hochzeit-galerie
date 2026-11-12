// Package config liest die Konfiguration aus Umgebungsvariablen (siehe .env.example).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type Config struct {
	Addr string
	// Token ist der geheime Teil der Galerie-Adresse: /photos/<Token>
	Token string
	// Password schützt Galerie und ZIP-Download; leer = Galerie offen und kein ZIP-Download
	Password string
	// DataDir ist das Datenverzeichnis, aufgebaut wie das der alten Java-Fassung: wedding.db und
	// assets/photos/<Token>/ mit den Fotos. Es lässt sich von dort einfach herüberkopieren.
	DataDir string
	// PhotosDir enthält die Originale; was dort liegt, zeigt die Galerie
	PhotosDir string
	// CacheDir nimmt die verkleinerten Fassungen auf (thumbs/, large/); lässt sich jederzeit löschen
	CacheDir string
	// LegacyDB ist die Datenbank der Java-Fassung. Gibt es sie, kommen Reihenfolge und Originalnamen von
	// dort (nur lesend); sonst wird nach Dateinamen sortiert.
	LegacyDB string

	Name1    string
	Name2    string
	Subtitle string
	ZipName  string

	// ImageWorkers begrenzt, wie viele Fotos gleichzeitig verkleinert werden. Jedes kostet beim
	// Dekodieren einige zehn MB Speicher.
	ImageWorkers int
	// ForceSecureCookie setzt das Secure-Flag auch ohne erkennbares HTTPS
	ForceSecureCookie bool
}

func Load() (Config, error) {
	cfg := Config{
		Addr:              env("ADDR", ":8080"),
		Token:             env("PHOTOS_TOKEN", ""),
		Password:          strings.TrimSpace(os.Getenv("PHOTOS_PASSWORD")),
		DataDir:           env("DATA_DIR", "data"),
		Name1:             env("COUPLE_NAME_1", "Julia"),
		Name2:             env("COUPLE_NAME_2", "Danny"),
		Subtitle:          env("GALLERY_SUBTITLE", "Die schönsten Momente unserer Hochzeit"),
		ZipName:           env("ZIP_NAME", "Hochzeitsfotos.zip"),
		ImageWorkers:      envInt("IMAGE_WORKERS", min(2, runtime.NumCPU())),
		ForceSecureCookie: envBool("SECURE_COOKIE", false),
	}
	if cfg.Token == "" {
		return cfg, errors.New("PHOTOS_TOKEN fehlt (z. B. openssl rand -hex 24)")
	}
	if strings.ContainsAny(cfg.Token, "/?#% ") {
		return cfg, errors.New("PHOTOS_TOKEN darf nur Zeichen enthalten, die in einer Adresse ungeschützt stehen können")
	}
	// Dieselben Orte wie in der Java-Fassung (app.assets-dir = data/assets)
	cfg.PhotosDir = env("PHOTOS_DIR", filepath.Join(cfg.DataDir, "assets", "photos", cfg.Token))
	cfg.CacheDir = env("CACHE_DIR", cfg.PhotosDir)
	cfg.LegacyDB = env("LEGACY_DB", filepath.Join(cfg.DataDir, "wedding.db"))
	if strings.EqualFold(cfg.LegacyDB, "off") {
		cfg.LegacyDB = ""
	}
	if cfg.ImageWorkers < 1 {
		return cfg, fmt.Errorf("IMAGE_WORKERS muss mindestens 1 sein, ist %d", cfg.ImageWorkers)
	}
	return cfg, nil
}

// Protected sagt, ob die Galerie ein Passwort hat.
func (c Config) Protected() bool {
	return c.Password != ""
}

func env(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	// podman-compose setzt leere Vorgaben teils nicht ein und reicht "${…}" wörtlich durch
	if strings.HasPrefix(value, "${") {
		slog.Warn("Wert sieht nach nicht ersetzter Variable aus und wird ignoriert", "key", key)
		return fallback
	}
	if value == "" {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) int {
	value := env(key, "")
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		slog.Warn("keine Zahl, nehme die Vorgabe", "key", key, "value", value, "default", fallback)
		return fallback
	}
	return n
}

func envBool(key string, fallback bool) bool {
	value := env(key, "")
	if value == "" {
		return fallback
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		slog.Warn("kein Wahrheitswert, nehme die Vorgabe", "key", key, "value", value, "default", fallback)
		return fallback
	}
	return b
}

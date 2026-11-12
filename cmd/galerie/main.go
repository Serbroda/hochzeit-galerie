// Command galerie liefert die Fotogalerie der Hochzeit aus. Ohne Datenbank: Was im Fotoordner liegt,
// zeigt die Galerie; die Einstellungen kommen aus Umgebungsvariablen (siehe .env.example).
//
//	galerie                 startet den Server
//	galerie link [BASIS]    zeigt den Link, der ohne Passworteingabe in die Galerie führt
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Serbroda/hochzeit-galerie/internal/config"
	"github.com/Serbroda/hochzeit-galerie/internal/gallery"
	"github.com/Serbroda/hochzeit-galerie/internal/images"
	"github.com/Serbroda/hochzeit-galerie/internal/server"
	"github.com/Serbroda/hochzeit-galerie/ui"
)

// version wird beim Bauen gesetzt: -ldflags "-X main.version=1.2.3"
var version = "dev"

func main() {
	setupLogging()
	if err := run(os.Args[1:]); err != nil {
		slog.Error("galerie beendet", "error", err)
		os.Exit(1)
	}
}

func setupLogging() {
	level := slog.LevelInfo
	if err := level.UnmarshalText([]byte(os.Getenv("LOG_LEVEL"))); err != nil {
		level = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
}

func run(args []string) error {
	if err := config.LoadDotenv(); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	lib := gallery.New(cfg.PhotosDir, cfg.LegacyDB)
	cache := images.NewCache(cfg.CacheDir, cfg.ImageWorkers)
	srv, err := server.New(cfg, lib, cache, ui.Templates(), ui.Static())
	if err != nil {
		return err
	}

	if len(args) > 0 {
		switch args[0] {
		case "link":
			base := ""
			if len(args) > 1 {
				base = args[1]
			}
			fmt.Println(srv.DirectLink(base))
			return nil
		default:
			return fmt.Errorf("unbekannter Befehl %q (bekannt: link)", args[0])
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Sobald neue Fotos im Ordner auftauchen, entstehen ihre Vorschaubilder im Hintergrund
	lib.OnChange(func(photos []gallery.Photo) {
		sources := make([]images.Source, len(photos))
		for i, p := range photos {
			sources[i] = lib.Source(p)
		}
		cache.Warm(ctx, sources)
	})
	// Erst jetzt einlesen: OnChange muss stehen, sonst entstünden die Vorschaubilder nicht vorab
	photos := lib.Photos()
	if legacy := lib.LegacyCount(); legacy > 0 {
		slog.Info("Reihenfolge und Namen aus der alten Datenbank", "db", cfg.LegacyDB, "photos", legacy)
	}
	if len(photos) == 0 {
		warnEmpty(cfg)
	}
	// Neue Dateien bemerkt die Galerie beim nächsten Abruf; ohne Besucher prüft das hier stündlich
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				lib.Photos()
			}
		}
	}()

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// Kein WriteTimeout: der ZIP-Download aller Originale dauert über eine langsame Leitung lange
	}
	errs := make(chan error, 1)
	go func() {
		slog.Info("galerie gestartet", "version", version, "addr", cfg.Addr, "photos", len(photos),
			"photos_dir", cfg.PhotosDir, "cache_dir", cfg.CacheDir, "protected", cfg.Protected(),
			"token", mask(cfg.Token))
		errs <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		slog.Info("fahre herunter")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
	}
	return nil
}

// warnEmpty hilft bei der häufigsten Ursache einer leeren Galerie: Der Fotoordner heißt nach dem Token
// (assets/photos/<Token>), und PHOTOS_TOKEN passt nicht zu dem Ordner, der tatsächlich daliegt. Die
// Galerie ist dann nur unter dem falschen Token erreichbar, und die alten QR-Codes führen ins 404.
func warnEmpty(cfg config.Config) {
	if _, err := os.Stat(cfg.PhotosDir); err != nil {
		slog.Warn("Fotoordner fehlt - die Galerie ist leer", "dir", cfg.PhotosDir)
	} else {
		slog.Warn("Fotoordner ist leer", "dir", cfg.PhotosDir)
	}
	parent := filepath.Dir(cfg.PhotosDir)
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && filepath.Join(parent, e.Name()) != filepath.Clean(cfg.PhotosDir) {
			slog.Warn("daneben liegt ein anderer Fotoordner - passt PHOTOS_TOKEN? Die Galerie antwortet nur unter /photos/<PHOTOS_TOKEN>",
				"dir", filepath.Join(parent, mask(e.Name())), "token", mask(cfg.Token))
		}
	}
}

// mask zeigt vom Token nur den Anfang - genug, um im Log zu sehen, welcher gilt.
func mask(token string) string {
	if len(token) <= 4 {
		return strings.Repeat("*", len(token))
	}
	return token[:4] + strings.Repeat("*", len(token)-4)
}

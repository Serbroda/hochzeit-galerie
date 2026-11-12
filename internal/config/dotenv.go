package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/joho/godotenv"
)

// dotenvFiles in der Reihenfolge ihres Vorrangs: ein Wert aus .env.local schlägt denselben aus .env.
var dotenvFiles = []string{".env.local", ".env"}

// LoadDotenv liest .env.local und .env aus dem Arbeitsverzeichnis, sofern vorhanden. Bereits gesetzte
// Umgebungsvariablen gewinnen immer - eine vergessene Datei überschreibt nie die Docker-Konfiguration.
func LoadDotenv() error {
	for _, file := range dotenvFiles {
		// godotenv.Load überschreibt nie, was schon gesetzt ist - daraus ergibt sich die Reihenfolge oben
		err := godotenv.Load(file)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("%s lesen: %w", file, err)
		}
		slog.Info("Umgebungsdatei geladen", "file", file)
	}
	return nil
}

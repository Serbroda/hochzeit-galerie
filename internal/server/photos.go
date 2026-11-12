package server

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Serbroda/hochzeit-galerie/internal/gallery"
	"github.com/Serbroda/hochzeit-galerie/internal/images"
)

// photoMaxAge: private, weil geschützter Inhalt nicht in geteilte Caches gehört. Ändert sich ein Foto
// doch, merkt der Browser es spätestens beim nächsten bedingten Abruf über Last-Modified.
const photoMaxAge = "private, max-age=2592000"

// photo prüft Token, Zugang und Dateinamen. Ohne Zugang gibt es 404, nicht 403 - wer den Link nicht
// kennt, soll nicht einmal erfahren, dass es das Foto gibt.
func (s *Server) photo(w http.ResponseWriter, r *http.Request) (gallery.Photo, bool) {
	if !s.access.validToken(r.PathValue("token")) || !s.access.hasAccess(r) {
		http.NotFound(w, r)
		return gallery.Photo{}, false
	}
	photo, ok := s.lib.Lookup(r.PathValue("file"))
	if !ok {
		http.NotFound(w, r)
		return gallery.Photo{}, false
	}
	return photo, true
}

// variant liefert die verkleinerte Fassung. Lässt sie sich nicht erzeugen (etwa bei HEIC), geht es zum
// Original weiter - dann lädt es zwar langsamer, aber es fehlt kein Foto.
func (s *Server) variant(v images.Variant) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		photo, ok := s.photo(w, r)
		if !ok {
			return
		}
		path, err := s.cache.Get(s.lib.Source(photo), v)
		if err != nil {
			target := "/photos/" + s.cfg.Token + "/original/" + url.PathEscape(photo.Name)
			http.Redirect(w, r, target, http.StatusFound)
			return
		}
		serveFile(w, r, path, "image/jpeg")
	}
}

// original liefert das Foto unverändert.
func (s *Server) original(w http.ResponseWriter, r *http.Request) {
	photo, ok := s.photo(w, r)
	if !ok {
		return
	}
	serveFile(w, r, s.lib.Path(photo), contentType(photo.Name))
}

func serveFile(w http.ResponseWriter, r *http.Request, path, ctype string) {
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", photoMaxAge)
	// ServeContent beantwortet If-Modified-Since und Range-Anfragen (abgebrochene Downloads, Videos)
	http.ServeContent(w, r, "", info.ModTime(), f)
}

func contentType(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".heic":
		return "image/heic"
	case ".heif":
		return "image/heif"
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "application/octet-stream"
}

// download schreibt alle Originale als ZIP, in der Reihenfolge der Galerie und unter ihren ursprünglichen
// Namen (sarakritzinger-1.jpg statt der UUID). Die Namen bekommen eine laufende Nummer, damit die
// Reihenfolge auch im entpackten Ordner erhalten bleibt - und weil Handys gern dieselben Namen vergeben.
//
// Fotos sind schon komprimiert - nochmal packen kostet nur Zeit, deshalb Store. Der Server hat
// absichtlich kein WriteTimeout: einige GB über eine langsame Leitung dauern.
func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	if !s.access.validToken(r.PathValue("token")) || !s.access.protected() || !s.access.hasAccess(r) {
		s.notFound(w, r)
		return
	}
	photos := s.lib.Photos()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": s.cfg.ZipName}))
	w.Header().Set("Cache-Control", "private, no-store")

	start := time.Now()
	if err := writeZip(w, s.lib, photos); err != nil {
		// Meist hat der Gast abgebrochen; der Status ist ohnehin schon verschickt
		slog.Info("ZIP-Download abgebrochen", "error", err, "duration", time.Since(start).Round(time.Second))
		return
	}
	slog.Info("ZIP-Download fertig", "photos", len(photos), "duration", time.Since(start).Round(time.Second))
}

func writeZip(w io.Writer, lib *gallery.Library, photos []gallery.Photo) error {
	zw := zip.NewWriter(w)
	width := max(3, len(strconv.Itoa(len(photos))))
	for i, photo := range photos {
		f, err := os.Open(lib.Path(photo))
		if errors.Is(err, os.ErrNotExist) {
			continue // zwischendurch gelöscht
		}
		if err != nil {
			return err
		}
		entry, err := zw.CreateHeader(&zip.FileHeader{
			Name:     fmt.Sprintf("%0*d_%s", width, i+1, safeName(photo.Title)),
			Method:   zip.Store,
			Modified: photo.ModTime,
		})
		if err == nil {
			_, err = io.Copy(entry, f)
		}
		f.Close()
		if err != nil {
			return err
		}
	}
	return zw.Close()
}

// safeName: keine Pfade und keine Zeichen, die Windows ablehnt.
func safeName(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`\/:*?"<>|`, r) || r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, name)
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return "foto"
	}
	return cleaned
}

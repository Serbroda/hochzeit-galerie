package server

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// minGzipSize: darunter lohnt das Packen nicht, ein TCP-Paket fasst ohnehin ~1400 Byte.
const minGzipSize = 1400

type pages struct {
	byName map[string]*template.Template
}

// loadPages parst jede Seite zusammen mit dem Rahmen aus layout.html.
func loadPages(templates fs.FS, assets *assets) (*pages, error) {
	funcs := template.FuncMap{
		"asset":      assets.url,
		"pathEscape": url.PathEscape,
		"add":        func(a, b int) int { return a + b },
		"sub":        func(a, b int) int { return a - b },
	}
	p := &pages{byName: map[string]*template.Template{}}
	for _, name := range []string{"gallery", "login", "notfound", "slideshow"} {
		t, err := template.New(name).Funcs(funcs).ParseFS(templates, "layout.html", name+".html")
		if err != nil {
			return nil, fmt.Errorf("Template %s: %w", name, err)
		}
		p.byName[name] = t
	}
	return p, nil
}

// render schreibt erst in einen Puffer: scheitert das Template, geht keine halbe Seite hinaus.
// Die Galerie-Seite schrumpft gepackt auf einen Bruchteil.
func (p *pages) render(w http.ResponseWriter, r *http.Request, status int, name string, data pageData) {
	p.execute(w, r, status, name, "layout", data)
}

// renderStandalone: Seiten ohne den gemeinsamen Rahmen (die Diashow).
func (p *pages) renderStandalone(w http.ResponseWriter, r *http.Request, name string, data pageData) {
	p.execute(w, r, http.StatusOK, name, name, data)
}

func (p *pages) execute(w http.ResponseWriter, r *http.Request, status int, name, root string, data pageData) {
	var buf bytes.Buffer
	if err := p.byName[name].ExecuteTemplate(&buf, root, data); err != nil {
		slog.Error("Seite nicht darstellbar", "page", name, "error", err)
		http.Error(w, "Interner Fehler", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "private, no-cache")
	h.Add("Vary", "Accept-Encoding")
	body := buf.Bytes()
	if len(body) >= minGzipSize && acceptsGzip(r) {
		var gz bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&gz, gzip.BestSpeed)
		_, _ = zw.Write(body)
		_ = zw.Close()
		body = gz.Bytes()
		h.Set("Content-Encoding", "gzip")
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		coding, params, _ := strings.Cut(part, ";")
		if !strings.EqualFold(strings.TrimSpace(coding), "gzip") {
			continue
		}
		q, found := strings.CutPrefix(strings.ReplaceAll(params, " ", ""), "q=")
		if !found {
			return true
		}
		v, err := strconv.ParseFloat(q, 64)
		return err == nil && v > 0
	}
	return false
}

// assets sind die eingebetteten statischen Dateien, beim Start einmal gehasht und gepackt.
type assets struct {
	files map[string]*asset
}

type asset struct {
	body  []byte
	gzip  []byte
	ctype string
	hash  string
}

func loadAssets(static fs.FS) (*assets, error) {
	a := &assets{files: map[string]*asset{}}
	err := fs.WalkDir(static, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := fs.ReadFile(static, name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		f := &asset{body: body, ctype: mime.TypeByExtension(path.Ext(name)), hash: hex.EncodeToString(sum[:])[:12]}
		if f.ctype == "" {
			f.ctype = "application/octet-stream"
		}
		// Bilder sind schon komprimiert, Text schrumpft auf einen Bruchteil
		if strings.HasPrefix(f.ctype, "text/") || strings.Contains(f.ctype, "javascript") {
			var gz bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&gz, gzip.BestCompression)
			_, _ = zw.Write(body)
			_ = zw.Close()
			if gz.Len() < len(body) {
				f.gzip = gz.Bytes()
			}
		}
		a.files[name] = f
		return nil
	})
	return a, err
}

// url hängt den Inhalts-Hash an: so darf der Browser die Datei für immer behalten und bekommt nach
// einer Änderung trotzdem sofort die neue.
func (a *assets) url(name string) (string, error) {
	f, ok := a.files[name]
	if !ok {
		return "", fmt.Errorf("unbekannte statische Datei %q", name)
	}
	return "/static/" + name + "?v=" + f.hash, nil
}

func (a *assets) serve(w http.ResponseWriter, r *http.Request) {
	f, ok := a.files[r.PathValue("path")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	etag := `"` + f.hash + `"`
	h.Set("ETag", etag)
	h.Set("Content-Type", f.ctype)
	h.Add("Vary", "Accept-Encoding")
	if r.URL.Query().Get("v") == f.hash {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		// Ohne Versionsangabe, etwa das Papier aus dem CSS: einen Tag, danach kurz nachfragen
		h.Set("Cache-Control", "public, max-age=86400")
	}
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body := f.body
	if f.gzip != nil && acceptsGzip(r) {
		body = f.gzip
		h.Set("Content-Encoding", "gzip")
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// Package server liefert die Galerie aus: die Seite unter /photos/<Token>, die Fotos in drei Größen,
// alle Originale als ZIP und die statischen Dateien.
package server

import (
	"io/fs"
	"net/http"
	"strconv"

	"github.com/Serbroda/hochzeit-galerie/internal/config"
	"github.com/Serbroda/hochzeit-galerie/internal/gallery"
	"github.com/Serbroda/hochzeit-galerie/internal/images"
)

// staticPageSize: Fotos pro Seite der Fassung ohne JavaScript.
const staticPageSize = 60

type Server struct {
	cfg    config.Config
	access access
	lib    *gallery.Library
	cache  *images.Cache
	pages  *pages
	assets *assets
}

func New(cfg config.Config, lib *gallery.Library, cache *images.Cache, templates, static fs.FS) (*Server, error) {
	assets, err := loadAssets(static)
	if err != nil {
		return nil, err
	}
	pages, err := loadPages(templates, assets)
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:    cfg,
		access: access{token: cfg.Token, password: cfg.Password, forceSecure: cfg.ForceSecureCookie},
		lib:    lib,
		cache:  cache,
		pages:  pages,
		assets: assets,
	}, nil
}

// DirectLink führt ohne Passworteingabe in die Galerie.
func (s *Server) DirectLink(baseURL string) string {
	return s.access.directLink(baseURL)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// Dieselben Adressen wie in der Java-Fassung - verteilte QR-Codes und Links bleiben gültig
	// Die blanke Domain führt zur Galerie; dort fragt die Passwortseite. Ohne Passwort bliebe dann nichts,
	// was Fremde abhält - in dem Fall bleibt die Adresse geheim und / antwortet mit 404.
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if !s.access.protected() {
			s.notFound(w, r)
			return
		}
		http.Redirect(w, r, "/photos/"+s.cfg.Token, http.StatusFound)
	})
	mux.HandleFunc("GET /photos/{token}", s.gallery)
	mux.HandleFunc("GET /photos/{token}/slideshow", s.slideshow)
	mux.HandleFunc("POST /photos/{token}/unlock", s.unlock)
	mux.HandleFunc("GET /photos/{token}/thumb/{file}", s.variant(images.Thumb))
	mux.HandleFunc("GET /photos/{token}/large/{file}", s.variant(images.Large))
	mux.HandleFunc("GET /photos/{token}/original/{file}", s.original)
	mux.HandleFunc("GET /photos/{token}/download", s.download)
	mux.HandleFunc("GET /static/{path...}", s.assets.serve)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { s.notFound(w, r) })
	return s.logRequests(securityHeaders(mux))
}

// item ist eine Kachel: Dateiname und, aus der alten Datenbank, wer das Foto hochgeladen hat.
type item struct {
	File string `json:"file"`
	By   string `json:"by,omitempty"`
}

type pageData struct {
	Name1    string
	Name2    string
	Subtitle string
	Token    string
	// Scripts bindet Alpine und app.js ein - nur die Galerie braucht sie
	Scripts bool
	Error   string
	// ReturnTo: wohin es nach der Passworteingabe geht (Galerie oder Diashow)
	ReturnTo string

	Count           int
	Items           []item
	DownloadEnabled bool
	StaticPhotos    []item
	StaticPage      int
	StaticPages     int
}

func (s *Server) data() pageData {
	return pageData{Name1: s.cfg.Name1, Name2: s.cfg.Name2, Subtitle: s.cfg.Subtitle, Token: s.cfg.Token}
}

func (s *Server) items() []item {
	photos := s.lib.Photos()
	items := make([]item, len(photos))
	for i, p := range photos {
		items[i] = item{File: p.Name, By: p.By}
	}
	return items
}

func (s *Server) gallery(w http.ResponseWriter, r *http.Request) {
	if !s.access.validToken(r.PathValue("token")) {
		s.notFound(w, r)
		return
	}
	if s.gate(w, r) {
		return
	}

	items := s.items()
	data := s.data()
	data.Scripts = true
	data.Count = len(items)
	// Die Kacheln baut die Seite selbst, nach und nach beim Scrollen - 600 Fotos als fertiges HTML wären
	// fast 2 MB. Nur was die Kachel braucht.
	data.Items = items
	data.DownloadEnabled = s.access.protected()

	// Ohne JavaScript: eine Seite mit festen Kacheln im <noscript>, durchblättert über ?seite=.
	data.StaticPages = max(1, (len(items)+staticPageSize-1)/staticPageSize)
	data.StaticPage = min(max(atoi(r.URL.Query().Get("seite"), 1), 1), data.StaticPages)
	from := (data.StaticPage - 1) * staticPageSize
	data.StaticPhotos = items[min(from, len(items)):min(len(items), from+staticPageSize)]

	s.pages.render(w, r, http.StatusOK, "gallery", data)
}

// slideshow zeigt die Fotos bildschirmfüllend nacheinander - für einen Fernseher oder Beamer.
func (s *Server) slideshow(w http.ResponseWriter, r *http.Request) {
	if !s.access.validToken(r.PathValue("token")) {
		s.notFound(w, r)
		return
	}
	if s.gate(w, r) {
		return
	}
	data := s.data()
	data.Items = s.items()
	s.pages.renderStandalone(w, r, "slideshow", data)
}

// flashCookie trägt die Meldung "falsches Passwort" über die Weiterleitung nach dem Formular - wie das
// Flash-Attribut der Java-Fassung. So steht danach wieder die Galerie-Adresse in der Adresszeile.
const flashCookie = "photos_flash"

// gate ist der Passwortschutz vor Galerie und Diashow. Gibt true zurück, wenn die Antwort schon
// geschrieben ist. Mit ?pass=<Base64> im Link wird direkt freigeschaltet und auf die Adresse ohne
// Parameter umgeleitet, damit das Passwort nicht in der Adresszeile stehen bleibt.
func (s *Server) gate(w http.ResponseWriter, r *http.Request) bool {
	if !s.access.protected() {
		return false
	}
	query := r.URL.Query()
	hasPass := query.Has("pass")
	if hasPass && s.access.matchesBase64(query.Get("pass")) {
		s.access.grant(w, r)
		http.Redirect(w, r, r.URL.Path, http.StatusFound)
		return true
	}
	if s.access.hasAccess(r) {
		if hasPass {
			http.Redirect(w, r, r.URL.Path, http.StatusFound)
			return true
		}
		return false
	}
	data := s.data()
	data.ReturnTo = r.URL.Path
	if hasPass {
		data.Error = "Der Link enthält kein gültiges Passwort."
	}
	if _, err := r.Cookie(flashCookie); err == nil {
		data.Error = "Das Passwort stimmt nicht."
		http.SetCookie(w, &http.Cookie{Name: flashCookie, Path: "/photos/", MaxAge: -1})
	}
	s.pages.render(w, r, http.StatusOK, "login", data)
	return true
}

// unlock nimmt das Passwort aus dem Eingabeformular entgegen; danach zurück zur Galerie oder Diashow.
func (s *Server) unlock(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if !s.access.validToken(token) {
		s.notFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	// Nur Ziele innerhalb der Galerie - sonst ließe sich hierüber auf fremde Seiten umleiten
	target := "/photos/" + token
	if r.PostFormValue("returnTo") == target+"/slideshow" {
		target += "/slideshow"
	}
	if s.access.matches(r.PostFormValue("password")) {
		s.access.grant(w, r)
	} else if s.access.protected() {
		http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "1", Path: "/photos/", MaxAge: 60, HttpOnly: true,
			SameSite: http.SameSiteLaxMode})
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.pages.render(w, r, http.StatusNotFound, "notfound", s.data())
}

func atoi(value string, fallback int) int {
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

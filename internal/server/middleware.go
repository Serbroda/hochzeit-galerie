package server

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		// Der Token steht in der Adresse - er soll nicht als Referrer bei fremden Seiten landen
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// logRequests schreibt eine Zeile pro Anfrage, mit unkenntlich gemachtem Token und ohne Query (darin
// stünde sonst das Passwort aus ?pass=). Fotos und statische Dateien nur auf Debug - ein Besuch der
// Galerie lädt Hunderte davon.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		path := strings.ReplaceAll(r.URL.Path, s.cfg.Token, "***")
		level := slog.LevelInfo
		if strings.HasPrefix(path, "/static/") || path == "/health" || isPhotoPath(path) {
			level = slog.LevelDebug
		}
		slog.Log(r.Context(), level, "request", "method", r.Method, "path", path, "status", rec.status,
			"bytes", rec.bytes, "duration", time.Since(start).Round(time.Millisecond), "ip", clientIP(r))
	})
}

func isPhotoPath(path string) bool {
	for _, v := range []string{"/thumb/", "/large/", "/original/"} {
		if strings.Contains(path, v) {
			return true
		}
	}
	return false
}

// clientIP: hinter dem Reverse Proxy steht die echte Adresse in X-Forwarded-For, nur fürs Log.
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		first, _, _ := strings.Cut(fwd, ",")
		return strings.TrimSpace(first)
	}
	return r.RemoteAddr
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(status int) {
	if !s.wroteHeader {
		s.status = status
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(status)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wroteHeader = true
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}

// ReadFrom reicht an den echten ResponseWriter durch: nur so verschickt http.ServeContent Fotos per
// sendfile direkt aus dem Dateisystem, statt sie durch einen Puffer zu kopieren.
func (s *statusRecorder) ReadFrom(src io.Reader) (int64, error) {
	s.wroteHeader = true
	var n int64
	var err error
	if rf, ok := s.ResponseWriter.(io.ReaderFrom); ok {
		n, err = rf.ReadFrom(src)
	} else {
		n, err = io.Copy(struct{ io.Writer }{s.ResponseWriter}, src)
	}
	s.bytes += n
	return n, err
}

func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

// Passwortschutz der Galerie. Der Link allein reicht nicht: wer zufällig darauf stößt, soll die Bilder
// nicht sehen. Nach der richtigen Eingabe merkt sich der Browser den Zugang ein Jahr lang in einem
// Cookie - darin steht nicht das Passwort, sondern ein Hash davon. Wird Passwort oder Token geändert,
// gelten alte Cookies nicht mehr.
//
// Name und Wert des Cookies sind dieselben wie in der früheren Java-Fassung: Wer die Galerie dort schon
// freigeschaltet hat, muss das Passwort nicht noch einmal eingeben.
//
// Ist kein Passwort gesetzt, ist die Galerie offen (und es gibt keinen ZIP-Download).

const accessCookie = "photos_access"

const cookieLifetime = 365 * 24 * time.Hour

type access struct {
	token    string
	password string
	// forceSecure setzt das Secure-Flag immer, sonst nur bei erkennbarem HTTPS
	forceSecure bool
}

func (a access) protected() bool {
	return a.password != ""
}

// validToken vergleicht in konstanter Zeit, damit die Antwortdauer nichts über den Token verrät.
func (a access) validToken(candidate string) bool {
	return equal(candidate, a.token)
}

// hasAccess: offen oder per Cookie freigeschaltet.
func (a access) hasAccess(r *http.Request) bool {
	if !a.protected() {
		return true
	}
	expected := a.cookieValue()
	for _, c := range r.CookiesNamed(accessCookie) {
		if equal(c.Value, expected) {
			return true
		}
	}
	return false
}

func (a access) matches(candidate string) bool {
	return a.protected() && equal(strings.TrimSpace(candidate), a.password)
}

// matchesBase64 prüft das Passwort aus dem Link-Parameter pass. Akzeptiert die normale und die
// URL-sichere Variante, mit und ohne Auffüll-Zeichen - und ein '+', das unterwegs zum Leerzeichen
// geworden ist.
func (a access) matchesBase64(encoded string) bool {
	cleaned := strings.TrimSpace(encoded)
	if cleaned == "" {
		return false
	}
	cleaned = strings.NewReplacer(" ", "+", "-", "+", "_", "/", "=", "").Replace(cleaned)
	decoded, err := base64.RawStdEncoding.DecodeString(cleaned)
	if err != nil {
		return false
	}
	return a.matches(string(decoded))
}

// directLink führt ohne Passworteingabe in die Galerie. URL-sicheres Base64 ohne Auffüllung, damit
// nichts kodiert werden muss und Messenger den Link nicht zerschneiden.
func (a access) directLink(baseURL string) string {
	link := strings.TrimRight(baseURL, "/") + "/photos/" + a.token
	if !a.protected() {
		return link
	}
	return link + "?pass=" + base64.RawURLEncoding.EncodeToString([]byte(a.password))
}

func (a access) grant(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     accessCookie,
		Value:    a.cookieValue(),
		Path:     "/",
		MaxAge:   int(cookieLifetime.Seconds()),
		HttpOnly: true,
		Secure:   a.forceSecure || isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// cookieValue hängt an Passwort und Token - ändert sich eines davon, ist der alte Zugang ungültig.
func (a access) cookieValue() string {
	sum := sha256.Sum256([]byte("hochzeit-fotos:" + a.token + ":" + a.password))
	return hex.EncodeToString(sum[:])
}

// equal vergleicht die Hashes, damit auch die Länge nichts verrät.
func equal(a, b string) bool {
	ha, hb := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

// isHTTPS erkennt HTTPS auch hinter einem Reverse Proxy, der X-Forwarded-Proto setzt.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

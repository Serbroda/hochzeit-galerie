package server

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Serbroda/hochzeit-galerie/internal/config"
	"github.com/Serbroda/hochzeit-galerie/internal/gallery"
	"github.com/Serbroda/hochzeit-galerie/internal/images"
	"github.com/Serbroda/hochzeit-galerie/ui"
)

const (
	token    = "fotos2026"
	password = "test123"
)

type testServer struct {
	*httptest.Server
	dir string
}

func newTestServer(t *testing.T, password string, names ...string) *testServer {
	t.Helper()
	dir := t.TempDir()
	photos := filepath.Join(dir, "photos")
	if err := os.Mkdir(photos, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	for _, name := range names {
		var buf bytes.Buffer
		if strings.HasSuffix(name, ".jpg") {
			_ = jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 40, 30)), nil)
		} else {
			buf.WriteString("kein lesbares Bild")
		}
		path := filepath.Join(photos, name)
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(path, old, old)
	}
	cfg := config.Config{
		Token: token, Password: password, PhotosDir: photos, CacheDir: filepath.Join(dir, "cache"),
		Name1: "Julia", Name2: "Danny", Subtitle: "Untertitel", ZipName: "Hochzeitsfotos.zip", ImageWorkers: 1,
	}
	srv, err := New(cfg, gallery.New(photos, ""), images.NewCache(cfg.CacheDir, 1), ui.Templates(), ui.Static())
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &testServer{Server: ts, dir: dir}
}

// client folgt keinen Weiterleitungen, damit Status und Location prüfbar bleiben.
func (ts *testServer) get(t *testing.T, path string, cookie *http.Cookie) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	return ts.do(t, req)
}

func (ts *testServer) do(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func body(t *testing.T, res *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func accessCookieFor(token, password string) *http.Cookie {
	return &http.Cookie{Name: accessCookie, Value: access{token: token, password: password}.cookieValue()}
}

func TestCookieIsCompatibleWithJavaVersion(t *testing.T) {
	// SHA-256 von "hochzeit-fotos:fotos2026:test123", wie es PhotoAccessService der Java-Fassung setzte
	want := "9eb6a49728042d2205705d4b854679f0fd99aaebed508141824f7018c80d5214"
	if got := (access{token: token, password: password}).cookieValue(); got != want {
		t.Fatalf("got %s", got)
	}
}

func TestUnknownTokenIsNotFound(t *testing.T) {
	ts := newTestServer(t, password, "a.jpg")
	for _, path := range []string{"/irgendwas", "/photos/falsch", "/photos/falsch/thumb/a.jpg", "/photos/falsch/download"} {
		if res := ts.get(t, path, accessCookieFor(token, password)); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d", path, res.StatusCode)
		}
	}
}

func TestPasswordGate(t *testing.T) {
	ts := newTestServer(t, password, "a.jpg")

	res := ts.get(t, "/photos/"+token, nil)
	if page := body(t, res); !strings.Contains(page, "mit einem Passwort geschützt") || strings.Contains(page, "a.jpg") {
		t.Fatal("ohne Cookie muss die Passwortseite kommen, ohne Fotos")
	}
	for _, path := range []string{"/thumb/a.jpg", "/large/a.jpg", "/original/a.jpg", "/download"} {
		if res := ts.get(t, "/photos/"+token+path, nil); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s ohne Zugang: %d", path, res.StatusCode)
		}
	}
	// Ein Cookie zum alten Passwort gilt nicht mehr
	if page := body(t, ts.get(t, "/photos/"+token, accessCookieFor(token, "altes"))); !strings.Contains(page, "Passwort") {
		t.Error("Cookie zu einem anderen Passwort darf nicht reichen")
	}
	if page := body(t, ts.get(t, "/photos/"+token, accessCookieFor(token, password))); !strings.Contains(page, `{"file":"a.jpg"}`) {
		t.Error("mit Cookie fehlt die Galerie")
	}
}

func TestUnlockForm(t *testing.T) {
	ts := newTestServer(t, password, "a.jpg")
	post := func(pw, returnTo string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/photos/"+token+"/unlock",
			strings.NewReader(url.Values{"password": {pw}, "returnTo": {returnTo}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return ts.do(t, req)
	}

	// Falsch: zurück zur Galerie-Adresse, die Meldung reist im Cookie mit (wie das Flash-Attribut in Java)
	res := post("falsch", "/photos/"+token)
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/photos/"+token {
		t.Fatalf("falsches Passwort: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	var flash *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == accessCookie {
			t.Fatal("falsches Passwort darf keinen Zugang geben")
		}
		if c.Name == flashCookie {
			flash = c
		}
	}
	if flash == nil {
		t.Fatal("keine Meldung")
	}
	res = ts.get(t, "/photos/"+token, flash)
	if page := body(t, res); !strings.Contains(page, "Das Passwort stimmt nicht.") {
		t.Error("Meldung fehlt auf der Passwortseite")
	}
	if page := body(t, ts.get(t, "/photos/"+token, nil)); strings.Contains(page, "Das Passwort stimmt nicht.") {
		t.Error("ohne Meldungs-Cookie keine Meldung")
	}

	res = post(" "+password+" ", "/photos/"+token)
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/photos/"+token {
		t.Fatalf("richtiges Passwort: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	cookies := res.Cookies()
	if len(cookies) != 1 || cookies[0].Value != accessCookieFor(token, password).Value || !cookies[0].HttpOnly ||
		cookies[0].MaxAge != 365*24*3600 || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].Path != "/" {
		t.Fatalf("Cookie: %+v", cookies)
	}

	// Zurück zur Diashow, aber nie auf fremde Ziele
	if loc := post(password, "/photos/"+token+"/slideshow").Header.Get("Location"); loc != "/photos/"+token+"/slideshow" {
		t.Errorf("Diashow: %s", loc)
	}
	if loc := post(password, "https://fremd.example").Header.Get("Location"); loc != "/photos/"+token {
		t.Errorf("fremdes Ziel: %s", loc)
	}
}

func TestSlideshow(t *testing.T) {
	ts := newTestServer(t, password, "b.jpg", "a.jpg")
	page := body(t, ts.get(t, "/photos/"+token+"/slideshow", nil))
	if !strings.Contains(page, `name="returnTo" value="/photos/`+token+`/slideshow"`) {
		t.Fatal("ohne Zugang: Passwortseite mit Rücksprung zur Diashow erwartet")
	}
	res := ts.get(t, "/photos/"+token+"/slideshow?pass="+base64.RawURLEncoding.EncodeToString([]byte(password)), nil)
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/photos/"+token+"/slideshow" {
		t.Fatalf("Direktlink zur Diashow: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	page = body(t, ts.get(t, "/photos/"+token+"/slideshow", accessCookieFor(token, password)))
	a, b := strings.Index(page, "/large/a.jpg"), strings.Index(page, "/large/b.jpg")
	if a < 0 || b < 0 || a > b || !strings.Contains(page, "Diashow") {
		t.Error("Diashow ohne Fotos oder falsch sortiert")
	}
}

func TestDirectLinkWithBase64Password(t *testing.T) {
	ts := newTestServer(t, "pässwort+/?", "a.jpg")
	raw := []byte("pässwort+/?")
	for _, pass := range []string{
		base64.RawURLEncoding.EncodeToString(raw),
		base64.StdEncoding.EncodeToString(raw),
		// '+' unterwegs zum Leerzeichen geworden
		strings.ReplaceAll(base64.StdEncoding.EncodeToString(raw), "+", " "),
	} {
		res := ts.get(t, "/photos/"+token+"?pass="+url.QueryEscape(pass), nil)
		if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/photos/"+token || len(res.Cookies()) != 1 {
			t.Errorf("pass=%q: %d %s", pass, res.StatusCode, res.Header.Get("Location"))
		}
	}

	res := ts.get(t, "/photos/"+token+"?pass=Zm9v", nil)
	if page := body(t, res); !strings.Contains(page, "Der Link enthält kein gültiges Passwort.") {
		t.Error("falsches Passwort im Link ohne Hinweis")
	}
	// Schon freigeschaltet: der Parameter verschwindet trotzdem aus der Adresszeile
	res = ts.get(t, "/photos/"+token+"?pass=egal", accessCookieFor(token, "pässwort+/?"))
	if res.StatusCode != http.StatusFound {
		t.Errorf("mit Cookie und pass: %d", res.StatusCode)
	}
}

func TestOpenGalleryWithoutPassword(t *testing.T) {
	ts := newTestServer(t, "", "a.jpg")
	page := body(t, ts.get(t, "/photos/"+token, nil))
	if !strings.Contains(page, `{"file":"a.jpg"}`) {
		t.Fatal("offene Galerie zeigt keine Fotos")
	}
	if strings.Contains(page, "Alle Fotos herunterladen") {
		t.Error("ohne Passwort gibt es keinen ZIP-Download")
	}
	if res := ts.get(t, "/photos/"+token+"/download", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("Download ohne Passwort: %d", res.StatusCode)
	}
	if res := ts.get(t, "/photos/"+token+"/thumb/a.jpg", nil); res.StatusCode != http.StatusOK {
		t.Errorf("Vorschaubild der offenen Galerie: %d", res.StatusCode)
	}
}

func TestGalleryPage(t *testing.T) {
	ts := newTestServer(t, password, "foto-10.jpg", "foto-2.jpg", "foto 1.jpg")
	page := body(t, ts.get(t, "/photos/"+token, accessCookieFor(token, password)))
	if !strings.Contains(page, `window.__photos = [{"file":"foto 1.jpg"},{"file":"foto-2.jpg"},{"file":"foto-10.jpg"}]`) {
		t.Errorf("Fotos fehlen oder falsch sortiert")
	}
	if strings.Contains(page, ts.dir) {
		t.Error("die Seite verrät Pfade des Servers")
	}
	for _, want := range []string{"3 Fotos", "Alle Fotos herunterladen", "/photos/" + token + "/original/foto%201.jpg", "<noscript>"} {
		if !strings.Contains(page, want) {
			t.Errorf("%q fehlt", want)
		}
	}
}

func TestStaticPagesWithoutJavaScript(t *testing.T) {
	var names []string
	for i := 1; i <= 130; i++ {
		names = append(names, fmt.Sprintf("f-%03d.jpg", i))
	}
	ts := newTestServer(t, password, names...)
	cookie := accessCookieFor(token, password)

	page := body(t, ts.get(t, "/photos/"+token+"?seite=3", cookie))
	if n := strings.Count(page, "/original/f-"); n != 10 {
		t.Errorf("Seite 3: %d Kacheln", n)
	}
	if !strings.Contains(page, "Seite 3 von 3") || !strings.Contains(page, "?seite=2") || strings.Contains(page, "Weiter") {
		t.Error("Blättern auf der letzten Seite falsch")
	}
	// Unsinnige Seiten landen auf der nächstgelegenen gültigen
	if page := body(t, ts.get(t, "/photos/"+token+"?seite=99", cookie)); !strings.Contains(page, "Seite 3 von 3") {
		t.Error("?seite=99")
	}
	if page := body(t, ts.get(t, "/photos/"+token+"?seite=abc", cookie)); !strings.Contains(page, "Seite 1 von 3") {
		t.Error("?seite=abc")
	}
}

func TestPhotosAreServedOnlyFromTheList(t *testing.T) {
	ts := newTestServer(t, password, "a.jpg")
	_ = os.WriteFile(filepath.Join(ts.dir, "geheim.jpg"), []byte("x"), 0o644)
	cookie := accessCookieFor(token, password)
	for _, path := range []string{"/original/..%2Fgeheim.jpg", "/original/%2E%2E%2Fgeheim.jpg", "/original/fehlt.jpg", "/thumb/..%2F..%2Fgeheim.jpg"} {
		if res := ts.get(t, "/photos/"+token+path, cookie); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d", path, res.StatusCode)
		}
	}
}

func TestVariantsAndCaching(t *testing.T) {
	ts := newTestServer(t, password, "a.jpg")
	cookie := accessCookieFor(token, password)
	for _, v := range []string{"thumb", "large", "original"} {
		res := ts.get(t, "/photos/"+token+"/"+v+"/a.jpg", cookie)
		if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "image/jpeg" ||
			res.Header.Get("Cache-Control") != photoMaxAge || res.Header.Get("Last-Modified") == "" {
			t.Errorf("%s: %d %v", v, res.StatusCode, res.Header)
		}
	}
	// Bedingter Abruf
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/photos/"+token+"/original/a.jpg", nil)
	req.AddCookie(cookie)
	req.Header.Set("If-Modified-Since", time.Now().UTC().Format(http.TimeFormat))
	if res := ts.do(t, req); res.StatusCode != http.StatusNotModified {
		t.Errorf("If-Modified-Since: %d", res.StatusCode)
	}
}

func TestUnreadableImageFallsBackToOriginal(t *testing.T) {
	ts := newTestServer(t, password, "iphone.heic")
	res := ts.get(t, "/photos/"+token+"/thumb/iphone.heic", accessCookieFor(token, password))
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/photos/"+token+"/original/iphone.heic" {
		t.Fatalf("%d %s", res.StatusCode, res.Header.Get("Location"))
	}
	res = ts.get(t, "/photos/"+token+"/original/iphone.heic", accessCookieFor(token, password))
	if res.Header.Get("Content-Type") != "image/heic" {
		t.Errorf("Content-Type %s", res.Header.Get("Content-Type"))
	}
}

func TestDownloadZipInGalleryOrder(t *testing.T) {
	ts := newTestServer(t, password, "b-10.jpg", "b-2.jpg", "a:b.jpg")
	res := ts.get(t, "/photos/"+token+"/download", accessCookieFor(token, password))
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "application/zip" ||
		res.Header.Get("Content-Disposition") != "attachment; filename=Hochzeitsfotos.zip" {
		t.Fatalf("%d %v", res.StatusCode, res.Header)
	}
	data := []byte(body(t, res))
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range zr.File {
		got = append(got, f.Name)
		if f.Method != zip.Store {
			t.Errorf("%s gepackt statt gespeichert", f.Name)
		}
		if f.UncompressedSize64 == 0 {
			t.Errorf("%s leer", f.Name)
		}
	}
	want := "001_a_b.jpg 002_b-2.jpg 003_b-10.jpg"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %v, want %s", got, want)
	}
}

func TestStaticAssets(t *testing.T) {
	ts := newTestServer(t, password)
	page := body(t, ts.get(t, "/photos/"+token, nil))
	start := strings.Index(page, "/static/css/app.css?v=")
	if start < 0 {
		t.Fatal("CSS-Link ohne Version")
	}
	link := page[start : start+strings.Index(page[start:], `"`)]

	req, _ := http.NewRequest(http.MethodGet, ts.URL+link, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	res := ts.do(t, req)
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Encoding") != "gzip" ||
		!strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("%d %v", res.StatusCode, res.Header)
	}
	req, _ = http.NewRequest(http.MethodGet, ts.URL+link, nil)
	req.Header.Set("If-None-Match", res.Header.Get("ETag"))
	if res := ts.do(t, req); res.StatusCode != http.StatusNotModified {
		t.Errorf("ETag: %d", res.StatusCode)
	}
	if res := ts.get(t, "/static/../go.mod", nil); res.StatusCode == http.StatusOK {
		t.Error("nur eingebettete Dateien")
	}
}

func TestGzipAndHeaders(t *testing.T) {
	ts := newTestServer(t, "", "a.jpg")
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/photos/"+token, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	res := ts.do(t, req)
	if res.Header.Get("Content-Encoding") != "gzip" {
		t.Error("Seite nicht gepackt")
	}
	if res.Header.Get("Referrer-Policy") != "same-origin" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("Header: %v", res.Header)
	}
}

func TestAcceptsGzip(t *testing.T) {
	for header, want := range map[string]bool{
		"": false, "gzip": true, "br, gzip;q=0.5": true, "gzip;q=0": false, "deflate": false, "GZIP": true,
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Accept-Encoding", header)
		if got := acceptsGzip(r); got != want {
			t.Errorf("%q: %v", header, got)
		}
	}
}

// TestOldDataDirectoryWorksAsIs: das Datenverzeichnis der Java-Fassung wird unverändert übernommen -
// UUID-Dateien, ihre Vorschaubilder unter thumbs/ und large/ und die wedding.db.
func TestOldDataDirectoryWorksAsIs(t *testing.T) {
	dir := t.TempDir()
	photos := filepath.Join(dir, "assets", "photos", token)
	_ = os.MkdirAll(filepath.Join(photos, "thumbs"), 0o755)
	db, err := sql.Open("sqlite", filepath.Join(dir, "wedding.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE photo (id INTEGER PRIMARY KEY AUTOINCREMENT, filename TEXT NOT NULL UNIQUE,
		original_name TEXT, uploaded_by TEXT, created_at TEXT NOT NULL);
		INSERT INTO photo (filename, original_name, uploaded_by, created_at) VALUES
		('2222-uuid.jpg', 'sarakritzinger-2.jpg', NULL, '2026-10-16 13:01:35'),
		('1111-uuid.jpg', 'sarakritzinger-1.jpg', NULL, '2026-10-16 13:02:35'),
		('3333-uuid.jpg', 'IMG_0001.jpg', 'Danny', '2026-09-09 21:09:54');`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"1111-uuid.jpg", "2222-uuid.jpg", "3333-uuid.jpg"} {
		var buf bytes.Buffer
		_ = jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 40, 30)), nil)
		_ = os.WriteFile(filepath.Join(photos, name), buf.Bytes(), 0o644) // frisch kopiert, Zeitstempel jetzt
	}
	_ = os.WriteFile(filepath.Join(photos, "thumbs", "1111-uuid.jpg"), []byte("vorhandenes Vorschaubild"), 0o644)

	t.Setenv("PHOTOS_TOKEN", token)
	t.Setenv("PHOTOS_PASSWORD", password)
	t.Setenv("DATA_DIR", dir)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PhotosDir != photos || cfg.CacheDir != photos || cfg.LegacyDB != filepath.Join(dir, "wedding.db") {
		t.Fatalf("Vorgaben passen nicht zum alten Verzeichnis: %+v", cfg)
	}
	srv, err := New(cfg, gallery.New(cfg.PhotosDir, cfg.LegacyDB), images.NewCache(cfg.CacheDir, 1), ui.Templates(), ui.Static())
	if err != nil {
		t.Fatal(err)
	}
	ts := &testServer{Server: httptest.NewServer(srv.Handler()), dir: dir}
	defer ts.Close()
	cookie := accessCookieFor(token, password)

	page := body(t, ts.get(t, "/photos/"+token, cookie))
	if !strings.Contains(page, `window.__photos = [{"file":"1111-uuid.jpg"},{"file":"2222-uuid.jpg"},{"file":"3333-uuid.jpg","by":"Danny"}]`) {
		t.Error("Reihenfolge oder Namen der alten Galerie fehlen")
	}
	// Dieselbe Adresse wie früher, und das vorhandene Vorschaubild statt eines neuen
	res := ts.get(t, "/photos/"+token+"/thumb/1111-uuid.jpg", cookie)
	if b := body(t, res); res.StatusCode != http.StatusOK || b != "vorhandenes Vorschaubild" {
		t.Errorf("thumb: %d %q", res.StatusCode, b)
	}
	if res := ts.get(t, "/photos/"+token+"/original/3333-uuid.jpg", cookie); res.StatusCode != http.StatusOK {
		t.Errorf("original: %d", res.StatusCode)
	}

	data := []byte(body(t, ts.get(t, "/photos/"+token+"/download", cookie)))
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if strings.Join(names, " ") != "001_sarakritzinger-1.jpg 002_sarakritzinger-2.jpg 003_IMG_0001.jpg" {
		t.Errorf("ZIP: %v", names)
	}
}

func TestRootRedirectsToGalleryOnlyWithPassword(t *testing.T) {
	ts := newTestServer(t, password, "a.jpg")
	res := ts.get(t, "/", nil)
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/photos/"+token {
		t.Fatalf("mit Passwort: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if page := body(t, ts.get(t, res.Header.Get("Location"), nil)); !strings.Contains(page, "mit einem Passwort geschützt") {
		t.Error("nach der Weiterleitung muss die Passwortseite kommen")
	}
	if res := ts.get(t, "/index.html", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("andere Pfade: %d", res.StatusCode)
	}

	open := newTestServer(t, "", "a.jpg")
	if res := open.get(t, "/", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("ohne Passwort darf / die Adresse nicht verraten: %d", res.StatusCode)
	}
}

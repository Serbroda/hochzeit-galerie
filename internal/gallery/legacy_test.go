package gallery

import (
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// legacyDB legt eine wedding.db im Schema der Java-Fassung an (Ausschnitt der Tabelle photo).
func legacyDB(t *testing.T, path string, rows [][4]string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE photo (
		id INTEGER PRIMARY KEY AUTOINCREMENT, filename TEXT NOT NULL UNIQUE, original_name TEXT,
		uploaded_by TEXT, version INTEGER NOT NULL DEFAULT 1,
		created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
		uploader_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows { // filename, original_name, uploaded_by, created_at
		var original, by any
		if r[1] != "" {
			original = r[1]
		}
		if r[2] != "" {
			by = r[2]
		}
		if _, err := db.Exec(`INSERT INTO photo (filename, original_name, uploaded_by, created_at) VALUES (?, ?, ?, ?)`,
			r[0], original, by, r[3]); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLegacyOrderNamesAndNewFiles(t *testing.T) {
	dir := t.TempDir()
	photos := filepath.Join(dir, "photos")
	_ = os.Mkdir(photos, 0o755)
	db := filepath.Join(dir, "wedding.db")
	legacyDB(t, db, [][4]string{
		{"aaaa.jpg", "sarakritzinger-2.jpg", "", "2026-10-16 13:01:35"},
		{"bbbb.jpg", "sarakritzinger-1.jpg", "", "2026-10-16 13:02:35"},
		// Gleiche Sekunde: die kleinere Id zuerst
		{"cccc.jpeg", "IMG_2949.jpeg", "Danny", "2026-09-09 21:09:54"},
		{"dddd.jpg", "danny_escobar.jpg", "Danny", "2026-09-09 21:09:54"},
		{"eeee.jpg", "", "", "2026-01-01 10:00:00"},            // ohne Originalnamen
		{"gelöscht.jpg", "weg.jpg", "", "2026-12-01 10:00:00"}, // Datei fehlt
	})
	old := time.Now().Add(-time.Hour)
	for _, name := range []string{"aaaa.jpg", "bbbb.jpg", "cccc.jpeg", "dddd.jpg", "eeee.jpg", "neu-10.jpg", "neu-2.jpg"} {
		write(t, filepath.Join(photos, name), old)
	}
	// Eben kopiert, ohne erhaltene Zeitstempel: steht in der Datenbank, also sofort sichtbar
	write(t, filepath.Join(photos, "aaaa.jpg"), time.Now())
	write(t, filepath.Join(photos, "frisch.jpg"), time.Now()) // nicht in der Datenbank: wartet noch

	lib := New(photos, db)
	var got []string
	for _, p := range lib.Photos() {
		got = append(got, p.Name)
	}
	want := []string{"bbbb.jpg", "aaaa.jpg", "cccc.jpeg", "dddd.jpg", "eeee.jpg", "neu-2.jpg", "neu-10.jpg"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	p, _ := lib.Lookup("cccc.jpeg")
	if p.Title != "IMG_2949.jpeg" || p.By != "Danny" || !p.Legacy || p.CacheKey != "cccc" {
		t.Errorf("Eintrag aus der Datenbank: %+v", p)
	}
	p, _ = lib.Lookup("eeee.jpg")
	if p.Title != "eeee.jpg" {
		t.Errorf("ohne Originalnamen: %q", p.Title)
	}
	p, _ = lib.Lookup("neu-2.jpg")
	if p.Legacy || p.Title != "neu-2.jpg" || p.By != "" {
		t.Errorf("neue Datei: %+v", p)
	}
	if lib.LegacyCount() != 6 {
		t.Errorf("LegacyCount %d", lib.LegacyCount())
	}
}

func TestNoLegacyDBMeansNameOrder(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "b.jpg"), time.Now().Add(-time.Hour))
	write(t, filepath.Join(dir, "a.jpg"), time.Now().Add(-time.Hour))
	lib := New(dir, filepath.Join(dir, "gibt-es-nicht.db"))
	if got := lib.Photos(); len(got) != 2 || got[0].Name != "a.jpg" {
		t.Fatalf("%v", got)
	}
}

func TestBrokenLegacyDBFallsBackToNames(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "wedding.db")
	_ = os.WriteFile(db, []byte("keine Datenbank"), 0o644)
	write(t, filepath.Join(dir, "b.jpg"), time.Now().Add(-time.Hour))
	write(t, filepath.Join(dir, "a.jpg"), time.Now().Add(-time.Hour))
	if got := New(dir, db).Photos(); len(got) != 2 || got[0].Name != "a.jpg" {
		t.Fatalf("%v", got)
	}
}

func TestLegacyDBIsNotModified(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "wedding.db")
	legacyDB(t, db, [][4]string{{"a.jpg", "x.jpg", "", "2026-01-01 10:00:00"}})
	before, _ := os.ReadFile(db)
	write(t, filepath.Join(dir, "a.jpg"), time.Now().Add(-time.Hour))
	New(dir, db).Photos()
	after, _ := os.ReadFile(db)
	if string(before) != string(after) {
		t.Fatal("wedding.db wurde verändert")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "wedding.db" && e.Name() != "a.jpg" {
			t.Errorf("unerwartete Datei %s neben der Datenbank", e.Name())
		}
	}
}

func TestCacheKeyCollision(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"foto.jpg", "foto.png", "anders.jpg"} {
		write(t, filepath.Join(dir, name), time.Now().Add(-time.Hour))
	}
	lib := New(dir, "")
	for name, want := range map[string]string{"foto.jpg": "foto.jpg", "foto.png": "foto.png", "anders.jpg": "anders"} {
		if p, _ := lib.Lookup(name); p.CacheKey != want {
			t.Errorf("%s: CacheKey %q, erwartet %q", name, p.CacheKey, want)
		}
	}
}

package gallery

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"time"

	_ "modernc.org/sqlite" // reines Go, kein CGO
)

// Die alte Java-Fassung hat die Fotos unter UUIDs abgelegt; Originalname, Name des Hochladenden und
// Reihenfolge standen nur in ihrer Datenbank. Liegt diese wedding.db noch neben den Fotos, liest die
// Galerie sie - nur lesend, verändert wird sie nie. So lässt sich das alte Datenverzeichnis einfach
// übernehmen: Dateinamen und damit alle Adressen bleiben gleich, die Reihenfolge auch.

type legacyEntry struct {
	// order ist die Position in der alten Galerie
	order    int
	original string
	by       string
}

// legacyIndex ordnet Dateinamen ihren Einträgen in der alten Datenbank zu.
type legacyIndex map[string]legacyEntry

func loadLegacy(path string) (legacyIndex, error) {
	// mode=ro: die Datei wird nicht angefasst, auch kein Journal angelegt
	dsn := "file:" + (&url.URL{Path: path}).EscapedPath() + "?mode=ro&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// Wie die Java-Galerie: neueste zuerst. Zeitstempel sind dort nur sekundengenau, bei Gleichstand
	// entscheidet die Id - sonst wäre die Reihenfolge zufällig.
	rows, err := db.Query(`SELECT filename, COALESCE(original_name, ''), COALESCE(uploaded_by, '')
		FROM photo ORDER BY created_at DESC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("Fotos aus %s lesen: %w", path, err)
	}
	defer rows.Close()

	index := legacyIndex{}
	for rows.Next() {
		var e legacyEntry
		var filename string
		if err := rows.Scan(&filename, &e.original, &e.by); err != nil {
			return nil, err
		}
		e.order = len(index)
		index[filename] = e
	}
	return index, rows.Err()
}

// legacySource merkt sich, welche Fassung der Datenbank gelesen wurde.
type legacySource struct {
	path    string
	modTime time.Time
	size    int64
	index   legacyIndex
}

// refresh liest die Datenbank neu, wenn sie sich geändert hat. Fehlt sie, gibt es keinen Index.
func (s *legacySource) refresh() (changed bool, err error) {
	if s.path == "" {
		return false, nil
	}
	info, err := os.Stat(s.path)
	if os.IsNotExist(err) {
		changed = s.index != nil
		s.index, s.modTime, s.size = nil, time.Time{}, 0
		return changed, nil
	}
	if err != nil {
		return false, err
	}
	if s.index != nil && info.ModTime().Equal(s.modTime) && info.Size() == s.size {
		return false, nil
	}
	index, err := loadLegacy(s.path)
	if err != nil {
		return false, err
	}
	s.index, s.modTime, s.size = index, info.ModTime(), info.Size()
	return true, nil
}

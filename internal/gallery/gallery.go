// Package gallery liefert die Fotos der Galerie - das ist schlicht der Inhalt des Fotoordners. Ein Foto
// kommt hinzu, indem man es in den Ordner legt, und verschwindet, wenn man es löscht.
//
// Reihenfolge: Liegt die Datenbank der alten Java-Fassung bei (legacy.go), stehen deren Fotos vorn, in
// derselben Reihenfolge wie dort. Alle übrigen folgen nach Dateinamen (sarakritzinger-2 vor -10).
package gallery

import (
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Serbroda/hochzeit-galerie/internal/images"
)

// MinAge: SFTP schreibt direkt unter dem endgültigen Namen. Eine Datei, die sich eben noch geändert hat,
// ist womöglich erst halb übertragen - ein Vorschaubild davon bliebe halb grau. Fotos aus der alten
// Datenbank sind davon ausgenommen: die waren dort schon vollständig, und nach einem Kopieren ohne
// erhaltene Zeitstempel sollen sie nicht für eine Minute verschwinden.
const MinAge = time.Minute

// rescanAfter: spätestens dann wird der Ordner neu gelesen, auch wenn er sich scheinbar nicht geändert
// hat - sonst tauchten Dateien, die beim letzten Mal noch zu jung waren, nie auf.
const rescanAfter = time.Minute

var extensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".heic": true, ".heif": true,
}

type Photo struct {
	// Name ist der Dateiname im Ordner und steht so in der Adresse
	Name string
	// Title ist der ursprüngliche Dateiname (aus der alten Datenbank), sonst Name - für das ZIP
	Title string
	// By ist der Name des Hochladenden aus der alten Datenbank, sonst leer
	By      string
	Size    int64
	ModTime time.Time
	// Legacy: das Foto stand in der alten Datenbank. Sein Inhalt ändert sich nie (UUID-Name), seine
	// Vorschaubilder gelten deshalb unabhängig vom Zeitstempel.
	Legacy bool
	// CacheKey benennt die Vorschaubilder: der Dateiname ohne Endung wie in der Java-Fassung, damit deren
	// thumbs/ und large/ weiter passen. Teilen sich zwei Fotos ihn (foto.jpg, foto.png), der volle Name.
	CacheKey string
}

// Library liest den Fotoordner und merkt sich das Ergebnis, bis sich der Ordner ändert.
type Library struct {
	dir    string
	now    func() time.Time
	legacy legacySource

	mu        sync.Mutex
	photos    []Photo
	byName    map[string]int
	dirMod    time.Time
	scannedAt time.Time
	// onChange erfährt von jeder neuen Liste, etwa um Vorschaubilder vorab zu erzeugen
	onChange func([]Photo)
}

// New liest die Fotos aus dir. legacyDB ist der Pfad der alten wedding.db; leer oder nicht vorhanden heißt:
// nur nach Dateinamen.
func New(dir, legacyDB string) *Library {
	return &Library{dir: dir, now: time.Now, legacy: legacySource{path: legacyDB}}
}

func (l *Library) Dir() string {
	return l.dir
}

// LegacyCount sagt, wie viele Fotos die alte Datenbank kennt (0 ohne Datenbank).
func (l *Library) LegacyCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refreshLocked()
	return len(l.legacy.index)
}

// OnChange registriert eine Funktion, die nach jedem Neueinlesen mit der Liste aufgerufen wird.
func (l *Library) OnChange(fn func([]Photo)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onChange = fn
}

// Photos liefert alle Fotos in Galerie-Reihenfolge. Das Ergebnis darf nicht verändert werden.
func (l *Library) Photos() []Photo {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refreshLocked()
	return l.photos
}

// Lookup findet ein Foto über seinen Dateinamen. Nur was in der Liste steht, wird ausgeliefert - so kann
// kein Pfad aus dem Ordner herausführen und kein halb übertragenes Foto erscheinen.
func (l *Library) Lookup(name string) (Photo, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refreshLocked()
	i, ok := l.byName[name]
	if !ok {
		return Photo{}, false
	}
	return l.photos[i], true
}

// Path ist der Pfad des Originals.
func (l *Library) Path(p Photo) string {
	return filepath.Join(l.dir, p.Name)
}

// Source beschreibt ein Foto für den Cache der verkleinerten Fassungen.
func (l *Library) Source(p Photo) images.Source {
	return images.Source{Name: p.Name, Key: p.CacheKey, Path: l.Path(p), ModTime: p.ModTime, Trusted: p.Legacy}
}

func (l *Library) refreshLocked() {
	now := l.now()
	legacyChanged, err := l.legacy.refresh()
	if err != nil && (l.byName == nil || now.Sub(l.scannedAt) >= rescanAfter) {
		slog.Warn("alte Datenbank nicht lesbar, sortiere nach Dateinamen", "db", l.legacy.path, "error", err)
	}
	info, err := os.Stat(l.dir)
	if err != nil {
		// Nicht bei jedem Abruf warnen; die Galerie ist dann eben leer
		if l.byName == nil || now.Sub(l.scannedAt) >= rescanAfter {
			slog.Warn("Fotoordner nicht lesbar", "dir", l.dir, "error", err)
			l.set(nil, time.Time{}, now)
		}
		return
	}
	if !legacyChanged && l.byName != nil && info.ModTime().Equal(l.dirMod) && now.Sub(l.scannedAt) < rescanAfter {
		return
	}
	photos, err := scan(l.dir, l.legacy.index, now)
	if err != nil {
		slog.Warn("Fotoordner nicht lesbar", "dir", l.dir, "error", err)
		return
	}
	changed := !slices.Equal(photos, l.photos)
	l.set(photos, info.ModTime(), now)
	if changed && l.onChange != nil {
		go l.onChange(photos)
	}
}

func (l *Library) set(photos []Photo, dirMod, now time.Time) {
	l.photos = photos
	l.byName = make(map[string]int, len(photos))
	for i, p := range photos {
		l.byName[p.Name] = i
	}
	l.dirMod = dirMod
	l.scannedAt = now
}

func scan(dir string, legacy legacyIndex, now time.Time) ([]Photo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	photos := make([]Photo, 0, len(entries))
	stems := map[string]int{}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !IsImage(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if !os.IsNotExist(err) { // zwischendurch gelöscht ist kein Fehler
				slog.Warn("Foto nicht lesbar", "file", entry.Name(), "error", err)
			}
			continue
		}
		p := Photo{Name: entry.Name(), Title: entry.Name(), Size: info.Size(), ModTime: info.ModTime()}
		if e, ok := legacy[p.Name]; ok {
			p.Legacy = true
			p.By = e.by
			if e.original != "" {
				p.Title = e.original
			}
		} else if now.Sub(info.ModTime()) < MinAge {
			continue
		}
		stems[stem(p.Name)]++
		photos = append(photos, p)
	}
	for i := range photos {
		photos[i].CacheKey = stem(photos[i].Name)
		if stems[photos[i].CacheKey] > 1 {
			photos[i].CacheKey = photos[i].Name
		}
	}
	slices.SortFunc(photos, func(a, b Photo) int {
		switch {
		case a.Legacy && b.Legacy:
			return compareInt(legacy[a.Name].order, legacy[b.Name].order)
		case a.Legacy:
			return -1
		case b.Legacy:
			return 1
		}
		return NaturalCompare(a.Name, b.Name)
	})
	return photos, nil
}

func stem(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// IsImage: versteckte Dateien sind Reste von macOS (._IMG_0001.jpg) oder rsync-Zwischenstände.
func IsImage(name string) bool {
	return !strings.HasPrefix(name, ".") && extensions[strings.ToLower(filepath.Ext(name))]
}

// NaturalCompare vergleicht ohne Rücksicht auf Groß- und Kleinschreibung und Ziffernfolgen als Zahlen:
// foto-2 kommt vor foto-10.
func NaturalCompare(a, b string) int {
	if c := naturalCompare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b) // eindeutige Reihenfolge, auch für Foto.jpg und foto.jpg
}

func naturalCompare(a, b string) int {
	for a != "" && b != "" {
		if isDigit(a[0]) && isDigit(b[0]) {
			na, ra := splitDigits(a)
			nb, rb := splitDigits(b)
			// Führende Nullen zählen nicht; die längere Zahl ist die größere
			ta, tb := strings.TrimLeft(na, "0"), strings.TrimLeft(nb, "0")
			if len(ta) != len(tb) {
				return compareInt(len(ta), len(tb))
			}
			if c := strings.Compare(ta, tb); c != 0 {
				return c
			}
			if len(na) != len(nb) { // 01 vor 1, damit nichts als gleich gilt
				return compareInt(len(nb), len(na))
			}
			a, b = ra, rb
			continue
		}
		if a[0] != b[0] {
			return compareInt(int(a[0]), int(b[0]))
		}
		a, b = a[1:], b[1:]
	}
	return compareInt(len(a), len(b))
}

func splitDigits(s string) (digits, rest string) {
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return s[:i], s[i:]
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

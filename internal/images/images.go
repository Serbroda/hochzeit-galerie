// Package images erzeugt die verkleinerten Fassungen der Fotos. Die Originale haben oft mehrere MB und
// 6000 px Kantenlänge; für Galerie und Großansicht reicht ein Bruchteil davon. Das Original bleibt
// unverändert und lässt sich weiterhin öffnen.
//
// Die Fassungen liegen wie in der Java-Fassung als JPEG unter <CacheDir>/thumbs/<Key>.jpg und
// <CacheDir>/large/<Key>.jpg - so passen die dort schon erzeugten weiter. Sie entstehen im Hintergrund
// (Warm), sobald ein Foto im Ordner auftaucht, und sonst beim ersten Abruf. Ist eine Fassung älter als ihr
// Original, wird sie neu erzeugt - ein ersetztes Foto bekommt so auch neue Vorschaubilder.
package images

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // Decoder registrieren
	"image/jpeg"
	_ "image/png"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

type Variant struct {
	Dir  string
	MaxW int
	MaxH int
}

var (
	// Thumb für die Galerie: Spaltenbreite mal Pixeldichte eines Handys; sehr hohe Panoramen werden begrenzt.
	Thumb = Variant{Dir: "thumbs", MaxW: 640, MaxH: 1280}
	// Large für die Großansicht: füllt einen Full-HD-Bildschirm, hat aber nur einen Bruchteil der Originalgröße.
	Large = Variant{Dir: "large", MaxW: 1920, MaxH: 1920}
)

// variants in der Reihenfolge der Erzeugung: Thumb entsteht aus Large, nicht noch einmal aus dem Original.
var variants = []Variant{Large, Thumb}

const jpegQuality = 82

// maxPixels schützt vor Dekompressionsbomben: eine kleine Datei, die sich zu Gigabytes entpackt.
const maxPixels = 150_000_000

// ErrUnsupported: das Format lässt sich nicht verkleinern (etwa HEIC) - dann zeigt die Seite das Original.
var ErrUnsupported = errors.New("Bildformat wird nicht unterstützt")

// Source ist ein Original, für das Fassungen gebraucht werden.
type Source struct {
	Name string
	// Key benennt die Fassungen; leer = Name
	Key     string
	Path    string
	ModTime time.Time
	// Trusted: vorhandene Fassungen gelten unabhängig vom Zeitstempel. Für Fotos der alten Fassung,
	// deren Inhalt sich nie ändert - nach einem Kopieren ohne erhaltene Zeitstempel wären die Originale
	// sonst womöglich jünger als ihre Vorschaubilder, und alles würde neu gerechnet.
	Trusted bool
}

func (s Source) key() string {
	if s.Key != "" {
		return s.Key
	}
	return s.Name
}

func (s Source) fresh(path string) bool {
	info, err := os.Stat(path)
	return err == nil && (s.Trusted || !info.ModTime().Before(s.ModTime))
}

type Cache struct {
	dir string
	// sem begrenzt gleichzeitige Erzeugungen: jedes dekodierte 24-MP-Foto belegt rund 40 MB
	sem chan struct{}

	mu       sync.Mutex
	inflight map[string]*flight
	// failed merkt sich Originale, die sich nicht lesen ließen, damit nicht jeder Abruf es erneut versucht
	failed map[string]time.Time

	warmMu  sync.Mutex
	warming bool
	pending []Source
}

type flight struct {
	done chan struct{}
	err  error
}

func NewCache(dir string, workers int) *Cache {
	return &Cache{
		dir:      dir,
		sem:      make(chan struct{}, max(1, workers)),
		inflight: map[string]*flight{},
		failed:   map[string]time.Time{},
	}
}

func (c *Cache) path(src Source, v Variant) string {
	return filepath.Join(c.dir, v.Dir, src.key()+".jpg")
}

// Get liefert den Pfad der verkleinerten Fassung und erzeugt sie bei Bedarf.
func (c *Cache) Get(src Source, v Variant) (string, error) {
	p := c.path(src, v)
	if src.fresh(p) {
		return p, nil
	}
	if err := c.ensure(src); err != nil {
		return "", err
	}
	return p, nil
}

// ensure erzeugt alle fehlenden Fassungen eines Fotos. Fragen mehrere Abrufe gleichzeitig nach
// demselben Foto, arbeitet nur einer, die übrigen warten auf sein Ergebnis.
func (c *Cache) ensure(src Source) error {
	c.mu.Lock()
	if t, ok := c.failed[src.Name]; ok && t.Equal(src.ModTime) {
		c.mu.Unlock()
		return ErrUnsupported
	}
	if f, ok := c.inflight[src.Name]; ok {
		c.mu.Unlock()
		<-f.done
		return f.err
	}
	f := &flight{done: make(chan struct{})}
	c.inflight[src.Name] = f
	c.mu.Unlock()

	c.sem <- struct{}{}
	f.err = c.generate(src)
	<-c.sem

	c.mu.Lock()
	delete(c.inflight, src.Name)
	if errors.Is(f.err, ErrUnsupported) {
		c.failed[src.Name] = src.ModTime
	}
	c.mu.Unlock()
	close(f.done)
	return f.err
}

func (c *Cache) missing(src Source) bool {
	for _, v := range variants {
		if !src.fresh(c.path(src, v)) {
			return true
		}
	}
	return false
}

func (c *Cache) generate(src Source) error {
	if !c.missing(src) {
		return nil // inzwischen von einem anderen Abruf erzeugt
	}
	start := time.Now()
	img, orientation, err := decode(src.Path)
	if err != nil {
		slog.Warn("Vorschaubild nicht möglich", "file", src.Name, "error", err)
		return err
	}
	for _, v := range variants {
		img = resize(img, orientation, v)
		orientation = 1 // die nächste Fassung entsteht aus dieser, und die ist schon gedreht
		p := c.path(src, v)
		if src.fresh(p) {
			continue // die andere Fassung fehlte, diese ist da
		}
		if err := writeJPEG(img, p); err != nil {
			return fmt.Errorf("%s für %s schreiben: %w", v.Dir, src.Name, err)
		}
	}
	slog.Debug("Vorschaubilder erzeugt", "file", src.Name, "duration", time.Since(start))
	return nil
}

// Warm erzeugt im Hintergrund alle fehlenden Fassungen, damit der erste Besuch nach dem Hochladen
// nicht darauf warten muss. Kommt währenddessen eine neue Liste, wird sie danach abgearbeitet.
func (c *Cache) Warm(ctx context.Context, sources []Source) {
	c.warmMu.Lock()
	if c.warming {
		c.pending = sources
		c.warmMu.Unlock()
		return
	}
	c.warming = true
	c.warmMu.Unlock()

	for {
		c.warm(ctx, sources)
		c.warmMu.Lock()
		if c.pending == nil || ctx.Err() != nil {
			c.warming = false
			c.pending = nil
			c.warmMu.Unlock()
			return
		}
		sources, c.pending = c.pending, nil
		c.warmMu.Unlock()
	}
}

func (c *Cache) warm(ctx context.Context, sources []Source) {
	var todo []Source
	for _, src := range sources {
		if c.missing(src) {
			todo = append(todo, src)
		}
	}
	if len(todo) == 0 {
		return
	}
	slog.Info("erzeuge Vorschaubilder", "count", len(todo))
	start := time.Now()
	failed := 0
	for i, src := range todo {
		if ctx.Err() != nil {
			return
		}
		if err := c.ensure(src); err != nil {
			failed++
		}
		if (i+1)%50 == 0 {
			slog.Info("Vorschaubilder", "done", i+1, "of", len(todo))
		}
	}
	slog.Info("Vorschaubilder fertig", "count", len(todo), "failed", failed, "duration", time.Since(start).Round(time.Second))
}

func decode(path string) (image.Image, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 1, err
	}
	defer f.Close()

	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		return nil, 1, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	if cfg.Width*cfg.Height > maxPixels {
		return nil, 1, fmt.Errorf("%w: %d×%d Pixel sind zu viel", ErrUnsupported, cfg.Width, cfg.Height)
	}
	orientation := 1
	if format == "jpeg" {
		if _, err := f.Seek(0, 0); err != nil {
			return nil, 1, err
		}
		orientation = Orientation(f)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, 1, err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		// Abgeschnittene Datei o. Ä.: beim nächsten Abruf nicht erneut versuchen, bis sich die Datei ändert
		return nil, 1, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	return img, orientation, nil
}

// resize verkleinert so, dass das Bild nach dem Drehen gemäß EXIF in maxW×maxH passt. Vergrößert wird nie.
func resize(src image.Image, orientation int, v Variant) image.Image {
	b := src.Bounds()
	// Bei 90°/270° vertauschen sich Breite und Höhe erst nach dem Drehen
	swapped := orientation >= 5 && orientation <= 8
	srcW, srcH := b.Dx(), b.Dy()
	if swapped {
		srcW, srcH = srcH, srcW
	}
	scale := min(1.0, float64(v.MaxW)/float64(srcW), float64(v.MaxH)/float64(srcH))
	w := max(1, int(float64(srcW)*scale+0.5))
	h := max(1, int(float64(srcH)*scale+0.5))
	// Maße vor dem Drehen
	pw, ph := w, h
	if swapped {
		pw, ph = h, w
	}

	// Bilineare Interpolation über große Faktoren flimmert; in Halbierungsschritten bleibt es sauber
	// (genau halbiert mittelt sie je 2×2 Pixel).
	cur := src
	for cur.Bounds().Dx() >= 2*pw && cur.Bounds().Dy() >= 2*ph {
		cb := cur.Bounds()
		half := image.NewRGBA(image.Rect(0, 0, cb.Dx()/2, cb.Dy()/2))
		draw.ApproxBiLinear.Scale(half, half.Bounds(), cur, cb, draw.Src, nil)
		cur = half
	}
	scaled := image.NewRGBA(image.Rect(0, 0, pw, ph))
	draw.BiLinear.Scale(scaled, scaled.Bounds(), cur, cur.Bounds(), draw.Src, nil)

	// JPEG kennt keine Transparenz - transparente PNGs bekommen weißen Grund statt schwarzem
	if !scaled.Opaque() {
		white := image.NewRGBA(scaled.Bounds())
		draw.Draw(white, white.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		draw.Draw(white, white.Bounds(), scaled, image.Point{}, draw.Over)
		scaled = white
	}
	return orient(scaled, orientation)
}

// orient dreht und spiegelt gemäß EXIF-Ausrichtung, sodass das Bild aufrecht steht.
func orient(src *image.RGBA, orientation int) *image.RGBA {
	if orientation < 2 || orientation > 8 {
		return src
	}
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	dw, dh := w, h
	if orientation >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range h {
		for x := range w {
			var dx, dy int
			switch orientation {
			case 2: // gespiegelt
				dx, dy = w-1-x, y
			case 3: // 180°
				dx, dy = w-1-x, h-1-y
			case 4: // senkrecht gespiegelt
				dx, dy = x, h-1-y
			case 5: // an der Hauptdiagonale gespiegelt
				dx, dy = y, x
			case 6: // 90° im Uhrzeigersinn
				dx, dy = h-1-y, x
			case 7: // an der Nebendiagonale gespiegelt
				dx, dy = h-1-y, w-1-x
			case 8: // 90° gegen den Uhrzeigersinn
				dx, dy = y, w-1-x
			}
			si := src.PixOffset(x, y)
			di := dst.PixOffset(dx, dy)
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}

// writeJPEG schreibt erst in eine Temp-Datei: ein paralleler Abruf soll nie ein halbes Bild sehen.
func writeJPEG(img image.Image, target string) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // nach dem Umbenennen ein No-op
	if err := jpeg.Encode(tmp, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}

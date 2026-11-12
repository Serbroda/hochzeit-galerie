package images

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// jpegWithOrientation baut ein JPEG mit EXIF-Ausrichtung, wie es ein Handy schreibt.
func jpegWithOrientation(t *testing.T, img image.Image, orientation uint16, order binary.ByteOrder) []byte {
	t.Helper()
	var plain bytes.Buffer
	if err := jpeg.Encode(&plain, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	var tiff bytes.Buffer
	if order == binary.LittleEndian {
		tiff.WriteString("II")
	} else {
		tiff.WriteString("MM")
	}
	_ = binary.Write(&tiff, order, uint16(42))
	_ = binary.Write(&tiff, order, uint32(8))
	_ = binary.Write(&tiff, order, uint16(2)) // zwei Einträge, die Ausrichtung als zweiter
	for _, e := range []struct{ tag, value uint16 }{{0x010F, 0}, {orientationTag, orientation}} {
		_ = binary.Write(&tiff, order, e.tag)
		_ = binary.Write(&tiff, order, uint16(3)) // SHORT
		_ = binary.Write(&tiff, order, uint32(1))
		_ = binary.Write(&tiff, order, e.value)
		_ = binary.Write(&tiff, order, uint16(0))
	}
	_ = binary.Write(&tiff, order, uint32(0))

	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	var out bytes.Buffer
	out.Write([]byte{0xFF, 0xD8})
	// Ein JFIF-Segment davor, wie bei vielen Kameras
	out.Write([]byte{0xFF, 0xE0, 0x00, 0x04, 0x00, 0x00})
	out.Write([]byte{0xFF, 0xE1})
	_ = binary.Write(&out, binary.BigEndian, uint16(len(payload)+2))
	out.Write(payload)
	out.Write(plain.Bytes()[2:])
	return out.Bytes()
}

func TestOrientation(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 4))
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for o := uint16(1); o <= 8; o++ {
			if got := Orientation(bytes.NewReader(jpegWithOrientation(t, img, o, order))); got != int(o) {
				t.Errorf("%v %d: got %d", order, o, got)
			}
		}
	}
	var plain bytes.Buffer
	_ = jpeg.Encode(&plain, img, nil)
	if got := Orientation(&plain); got != 1 {
		t.Errorf("ohne EXIF: %d", got)
	}
	if got := Orientation(bytes.NewReader([]byte("kein jpeg"))); got != 1 {
		t.Errorf("kein JPEG: %d", got)
	}
}

// TestOrientAllEight prüft gegen die EXIF-Spezifikation, wohin die gespeicherten Ecken oben links und
// oben rechts im aufrecht gezeigten Bild wandern. Zwei Ecken legen die Drehung eindeutig fest.
func TestOrientAllEight(t *testing.T) {
	const w, h = 3, 2
	topLeft := color.RGBA{255, 0, 0, 255}
	topRight := color.RGBA{0, 0, 255, 255}
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	src.Set(0, 0, topLeft)
	src.Set(w-1, 0, topRight)

	for orientation, want := range map[int][2]image.Point{
		1: {{0, 0}, {w - 1, 0}},
		2: {{w - 1, 0}, {0, 0}},
		3: {{w - 1, h - 1}, {0, h - 1}},
		4: {{0, h - 1}, {w - 1, h - 1}},
		5: {{0, 0}, {0, w - 1}},
		6: {{h - 1, 0}, {h - 1, w - 1}},
		7: {{h - 1, w - 1}, {h - 1, 0}},
		8: {{0, w - 1}, {0, 0}},
	} {
		got := orient(src, orientation)
		if got.RGBAAt(want[0].X, want[0].Y) != topLeft || got.RGBAAt(want[1].X, want[1].Y) != topRight {
			t.Errorf("Ausrichtung %d: Ecken nicht an %v", orientation, want)
		}
	}
}

func TestGenerateRotatesAndScales(t *testing.T) {
	dir := t.TempDir()
	// Quer gespeichert, aber mit Vermerk "90° drehen": links rot, rechts blau
	src := image.NewRGBA(image.Rect(0, 0, 3000, 1000))
	for y := range 1000 {
		for x := range 3000 {
			c := color.RGBA{255, 0, 0, 255}
			if x >= 1500 {
				c = color.RGBA{0, 0, 255, 255}
			}
			src.Set(x, y, c)
		}
	}
	original := filepath.Join(dir, "hochkant.jpg")
	if err := os.WriteFile(original, jpegWithOrientation(t, src, 6, binary.BigEndian), 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(original)
	cache := NewCache(filepath.Join(dir, "cache"), 1)
	s := Source{Name: "hochkant.jpg", Path: original, ModTime: info.ModTime()}

	large := decodeFile(t, must(cache.Get(s, Large)))
	if b := large.Bounds(); b.Dx() != 640 || b.Dy() != 1920 {
		t.Fatalf("Large %v, erwartet 640×1920", b)
	}
	// Nach 90° im Uhrzeigersinn liegt das ehemals linke Rot oben
	if r, _, b, _ := large.At(320, 100).RGBA(); r < b {
		t.Error("oben sollte rot sein - nicht gedreht?")
	}
	thumb := decodeFile(t, must(cache.Get(s, Thumb)))
	if b := thumb.Bounds(); b.Dx() != 427 || b.Dy() != 1280 {
		t.Fatalf("Thumb %v, erwartet 427×1280", b)
	}
}

func TestSmallImagesAreNotEnlargedAndTransparencyIsWhite(t *testing.T) {
	dir := t.TempDir()
	src := image.NewNRGBA(image.Rect(0, 0, 100, 50)) // komplett transparent
	var buf bytes.Buffer
	_ = png.Encode(&buf, src)
	original := filepath.Join(dir, "klein.png")
	_ = os.WriteFile(original, buf.Bytes(), 0o644)
	info, _ := os.Stat(original)

	thumb := decodeFile(t, must(NewCache(filepath.Join(dir, "cache"), 1).Get(Source{Name: "klein.png", Path: original, ModTime: info.ModTime()}, Thumb)))
	if b := thumb.Bounds(); b.Dx() != 100 || b.Dy() != 50 {
		t.Fatalf("%v, erwartet unverändert 100×50", b)
	}
	if r, g, b, _ := thumb.At(50, 25).RGBA(); r < 0xf000 || g < 0xf000 || b < 0xf000 {
		t.Error("transparent sollte weiß werden, nicht schwarz")
	}
}

func TestUnsupportedFormat(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "iphone.heic")
	_ = os.WriteFile(original, []byte("....ftypheic"), 0o644)
	info, _ := os.Stat(original)
	cache := NewCache(filepath.Join(dir, "cache"), 1)
	s := Source{Name: "iphone.heic", Path: original, ModTime: info.ModTime()}
	if _, err := cache.Get(s, Thumb); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
	// Zweiter Versuch kommt aus dem Gedächtnis, ohne die Datei erneut zu lesen
	_ = os.Remove(original)
	if _, err := cache.Get(s, Thumb); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("zweiter Versuch: %v", err)
	}
}

func TestReplacedOriginalGetsNewVariants(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "a.jpg")
	writeTestJPEG(t, original, 800, 600, time.Now().Add(-time.Hour))
	cache := NewCache(filepath.Join(dir, "cache"), 1)
	info, _ := os.Stat(original)
	cache.Warm(context.Background(), []Source{{Name: "a.jpg", Path: original, ModTime: info.ModTime()}})
	if b := decodeFile(t, cache.path(Source{Name: "a.jpg"}, Thumb)).Bounds(); b.Dx() != 640 {
		t.Fatalf("Warm: %v", b)
	}

	writeTestJPEG(t, original, 600, 800, time.Now().Add(time.Hour)) // ersetzt, jünger als das Vorschaubild
	info, _ = os.Stat(original)
	thumb := decodeFile(t, must(cache.Get(Source{Name: "a.jpg", Path: original, ModTime: info.ModTime()}, Thumb)))
	if b := thumb.Bounds(); b.Dx() != 600 || b.Dy() != 800 {
		t.Fatalf("altes Vorschaubild ausgeliefert: %v", b)
	}
}

func writeTestJPEG(t *testing.T, path string, w, h int, mod time.Time) {
	t.Helper()
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h)), nil)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(path, mod, mod)
}

func decodeFile(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func must(path string, err error) string {
	if err != nil {
		panic(err)
	}
	return path
}

// BenchmarkGenerate24MP: ein Foto der Fotografin hat etwa 6000×4000 Pixel.
// go test ./internal/images -bench . -run '^$'
func BenchmarkGenerate24MP(b *testing.B) {
	dir := b.TempDir()
	src := image.NewRGBA(image.Rect(0, 0, 6000, 4000))
	for i := range src.Pix {
		src.Pix[i] = byte(i * 7 % 251) // Muster statt Fläche, damit JPEG etwas zu tun hat
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, src, &jpeg.Options{Quality: 90})
	original := filepath.Join(dir, "gross.jpg")
	_ = os.WriteFile(original, buf.Bytes(), 0o644)
	info, _ := os.Stat(original)
	s := Source{Name: "gross.jpg", Path: original, ModTime: info.ModTime()}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = os.RemoveAll(filepath.Join(dir, "cache"))
		if _, err := NewCache(filepath.Join(dir, "cache"), 1).Get(s, Thumb); err != nil {
			b.Fatal(err)
		}
	}
}

// Fotos der alten Fassung: Nach einem Kopieren ohne erhaltene Zeitstempel ist das Original jünger als sein
// Vorschaubild. Das darf nicht dazu führen, dass alles neu gerechnet wird.
func TestTrustedVariantsAreKeptDespiteTimestamps(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "1234-uuid.jpg")
	writeTestJPEG(t, original, 800, 600, time.Now())
	for _, v := range []string{"thumbs", "large"} {
		_ = os.MkdirAll(filepath.Join(dir, v), 0o755)
		p := filepath.Join(dir, v, "1234-uuid.jpg")
		_ = os.WriteFile(p, []byte("alte Fassung"), 0o644)
		_ = os.Chtimes(p, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	}
	info, _ := os.Stat(original)
	cache := NewCache(dir, 1)
	src := Source{Name: "1234-uuid.jpg", Key: "1234-uuid", Path: original, ModTime: info.ModTime(), Trusted: true}
	cache.Warm(context.Background(), []Source{src})
	got, err := cache.Get(src, Thumb)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(got); string(b) != "alte Fassung" {
		t.Fatal("vorhandenes Vorschaubild wurde ersetzt")
	}
	if got != filepath.Join(dir, "thumbs", "1234-uuid.jpg") {
		t.Errorf("Pfad %s - nicht das Schema der Java-Fassung", got)
	}
}

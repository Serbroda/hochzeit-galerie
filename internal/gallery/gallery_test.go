package gallery

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestNaturalCompare(t *testing.T) {
	names := []string{"foto-10.jpg", "Foto-2.jpg", "foto-1.jpg", "foto-02.jpg", "IMG_0001.jpg", "a.jpg", "foto-2.jpg"}
	slices.SortFunc(names, NaturalCompare)
	want := []string{"a.jpg", "foto-1.jpg", "foto-02.jpg", "Foto-2.jpg", "foto-2.jpg", "foto-10.jpg", "IMG_0001.jpg"}
	if !slices.Equal(names, want) {
		t.Fatalf("got %v, want %v", names, want)
	}
}

func TestSaraKritzingerInNumberOrder(t *testing.T) {
	var names []string
	for _, n := range []string{"621", "10", "2", "1", "100"} {
		names = append(names, "sarakritzinger-"+n+".jpg")
	}
	slices.SortFunc(names, NaturalCompare)
	want := []string{"sarakritzinger-1.jpg", "sarakritzinger-2.jpg", "sarakritzinger-10.jpg", "sarakritzinger-100.jpg", "sarakritzinger-621.jpg"}
	if !slices.Equal(names, want) {
		t.Fatalf("got %v", names)
	}
}

func TestPhotosSkipsHiddenForeignAndFreshFiles(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-time.Hour)
	for _, name := range []string{"b-2.jpg", "b-10.JPG", "a.png", "._a.jpg", "notes.txt", "c.heic"} {
		write(t, filepath.Join(dir, name), old)
	}
	write(t, filepath.Join(dir, "uploading.jpg"), time.Now()) // eben erst angekommen
	if err := os.Mkdir(filepath.Join(dir, "thumbs.jpg"), 0o755); err != nil {
		t.Fatal(err)
	}

	lib := New(dir, "")
	var got []string
	for _, p := range lib.Photos() {
		got = append(got, p.Name)
	}
	want := []string{"a.png", "b-2.jpg", "b-10.JPG", "c.heic"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if _, ok := lib.Lookup("uploading.jpg"); ok {
		t.Error("halb übertragene Datei darf nicht ausgeliefert werden")
	}
	if _, ok := lib.Lookup("../b-2.jpg"); ok {
		t.Error("Pfade dürfen nicht aus dem Ordner führen")
	}

	// Eine Minute später ist die Datei fertig und taucht auf, auch ohne Änderung am Ordner
	lib.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if _, ok := lib.Lookup("uploading.jpg"); !ok {
		t.Error("fertig übertragene Datei fehlt")
	}
}

func TestOnChangeAfterNewPhoto(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "1.jpg"), time.Now().Add(-time.Hour))
	lib := New(dir, "")
	changes := make(chan []Photo, 4)
	lib.OnChange(func(p []Photo) { changes <- p })
	lib.Photos()
	if got := <-changes; len(got) != 1 {
		t.Fatalf("erste Liste: %d Fotos", len(got))
	}

	write(t, filepath.Join(dir, "2.jpg"), time.Now().Add(-time.Hour))
	lib.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if n := len(lib.Photos()); n != 2 {
		t.Fatalf("%d Fotos statt 2", n)
	}
	select {
	case got := <-changes:
		if len(got) != 2 {
			t.Fatalf("zweite Liste: %d Fotos", len(got))
		}
	case <-time.After(time.Second):
		t.Fatal("keine Benachrichtigung über das neue Foto")
	}
}

func TestMissingDirIsEmpty(t *testing.T) {
	if n := len(New(filepath.Join(t.TempDir(), "fehlt"), "").Photos()); n != 0 {
		t.Fatalf("%d Fotos", n)
	}
}

func write(t *testing.T, path string, mod time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

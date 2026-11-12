# AGENTS.md

Hinweise für Coding-Agenten (Claude Code, Codex & Co.) in diesem Repository.

Die Fotogalerie der Hochzeit: ein Go-Server (`cmd/galerie`, Standardbibliothek plus `golang.org/x/image`,
`godotenv` und `modernc.org/sqlite`) mit eingebetteten Templates (`html/template`), Alpine.js und Tailwind v4.
**Keine eigene Datenbank:** die Galerie ist der Inhalt von `PHOTOS_DIR`, die Einstellungen kommen aus der
Umgebung. Portiert aus dem Fotobereich der Spring-Boot-Hochzeitsseite (`../hochzeit`).

**Oberstes Ziel: alles bleibt wie in der Java-Fassung.** QR-Codes mit `/photos/<Token>?pass=<Base64>` sind
an Familie und Freunde verteilt; Adressen, Weiterleitungen, Cookie und Aussehen dürfen sich nicht ändern.
Das Datenverzeichnis der Java-Fassung (`wedding.db`, `assets/photos/<Token>/` mit UUID-Dateien, `thumbs/`,
`large/`) wird unverändert übernommen und muss ohne jeden Zwischenschritt funktionieren.

## Befehle

```bash
make dev      # Server auf :8080 mit .env.local
make test     # go test ./...
make check    # go vet, gofmt
make css      # Tailwind bauen - unminifiziert, so liegt es im Repo
```

Ein einzelner Test: `go test ./internal/server -run TestPasswordGate`.
Zum Ausprobieren `DATA_DIR` auf eine Kopie von `../hochzeit/data` im Scratchpad zeigen lassen (nie auf das Original).

## Pakete

- `cmd/galerie` — main: Konfiguration, Server mit Graceful Shutdown, Vorschaubilder im Hintergrund;
  `galerie link [Basis-URL]` zeigt den Direktlink mit Passwort
- `internal/config` — Umgebungsvariablen (`.env.example`), liest lokal auch `.env.local` und `.env`
- `internal/gallery` — liest den Fotoordner, merkt sich die Liste bis sich der Ordner ändert und liest
  spätestens nach einer Minute neu. `legacy.go` liest die alte `wedding.db` **nur lesend** (`mode=ro`):
  Reihenfolge `created_at DESC, id ASC` wie die Java-Galerie, Originalname, `uploaded_by`. Fotos, die dort
  nicht stehen, folgen nach Dateinamen (`NaturalCompare`)
- `internal/images` — verkleinerte Fassungen (Thumb 640×1280, Large 1920×1920, JPEG 82) mit EXIF-Drehung
- `internal/server` — Routen, Passwortschutz (`access.go`), Fotos und ZIP (`photos.go`), Seiten und
  statische Dateien (`render.go`)
- `ui` — `templates/` (eine Seite = `layout.html` + `<seite>.html`), `static/` (CSS, `app.js`, Alpine),
  `frontend/app.css` (Tailwind-Quelle)

## Regeln, die schon einmal wichtig waren

- **Adressen:** `TestOldDataDirectoryWorksAsIs`, `TestDirectLinkWithBase64Password`, `TestSlideshow` und
  `TestUnlockForm` halten das Verhalten der Java-Fassung fest (auch: falsches Passwort → Weiterleitung mit
  Meldung per Cookie, wie deren Flash-Attribut; `returnTo` nur Galerie oder Diashow).
- **Cookie-Kompatibilität:** Name `photos_access`, Wert `sha256("hochzeit-fotos:<Token>:<Passwort>")` als
  Hex — identisch zur Java-Fassung, damit Freischaltungen den Umzug überleben
  (`TestCookieIsCompatibleWithJavaVersion`). Nicht ändern, ohne das bewusst zu wollen.
- **`/` leitet zur Galerie** (`/photos/<Token>`), aber nur mit Passwort - ohne wäre die Galerie dann für
  jeden offen, der die Domain aufruft. Alle anderen unbekannten Pfade: 404.
- **Nur Dateien aus der Liste ausliefern:** Fotos werden über `Library.Lookup` gefunden, nie über einen aus
  der URL gebauten Pfad. Ohne Zugang gibt es 404, nicht 403.
- **Halb übertragene Dateien:** jünger als `gallery.MinAge` (1 Minute) erscheinen nicht — SFTP schreibt unter
  dem endgültigen Namen, ein Vorschaubild davon bliebe halb grau. Beim Kopieren von Testdaten `cp -p`.
- **Vorschaubilder** liegen wie in Java unter `<Fotoordner>/thumbs/<Dateiname ohne Endung>.jpg` (und
  `large/`). Sie gelten, solange sie nicht älter als ihr Original sind — außer bei Fotos aus der alten
  Datenbank (`Photo.Legacy`, `Source.Trusted`): deren Inhalt ändert sich nie, und nach einem `cp` ohne
  erhaltene Zeitstempel würde sonst alles neu gerechnet. Aus demselben Grund gilt `MinAge` für sie nicht.
- **EXIF-Drehung:** `TestOrientAllEight` prüft alle acht Fälle gegen die Spezifikation.
- **Kein WriteTimeout** im `http.Server`: der ZIP-Download (mehrere GB) dauert. Die ZIP-Einträge sind
  `Store`, nummeriert in Galerie-Reihenfolge.
- **Sendfile:** `statusRecorder` reicht `ReadFrom` durch, sonst kopiert `http.ServeContent` jedes Foto durch
  einen Puffer.
- **Galerie-Seite:** Die Kacheln entstehen im Browser aus `window.__photos` (`{file, by}`), 60 auf einmal.
  Mauerwerk per JS mit absoluter Positionierung in Lesereihenfolge — **kein** CSS-Grid mit 2-px-Zeilen und
  **keine** CSS-`columns` (füllen von oben nach unten). Ohne JavaScript greift das `<noscript>` mit 60
  quadratischen Kacheln pro Seite über `?seite=`.
- **Statische Dateien** bekommen `?v=<Inhalts-Hash>` (Template-Funktion `asset`) und sind dann ein Jahr
  cachebar; die Galerie-Seite selbst `no-cache`.
- **Logs:** Token im Pfad wird zu `***`, Query-Strings (darin stünde `?pass=`) werden nie geloggt.

## Texte

Deutsch, Gäste werden neutral oder in der Mehrzahl angesprochen. Code-Kommentare ebenfalls auf Deutsch.

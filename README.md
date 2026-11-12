# Hochzeitsgalerie

Die Fotogalerie unserer Hochzeit als kleiner Go-Server: ein Binary, keine eigene Datenbank. Was im
Fotoordner liegt, zeigt die Galerie unter `/photos/<Token>` — als Mauerwerk, das beim Scrollen nachlädt,
mit Großansicht zum Durchblättern, einer Fassung ohne JavaScript, Diashow und allen Originalen als ZIP. Ein
Passwort schützt Galerie und Download.

Hervorgegangen aus dem Fotobereich der früheren Hochzeitsseite (Spring Boot). **Alle Adressen bleiben
gleich** — die verteilten QR-Codes (`/photos/<Token>?pass=<Base64>`), die Diashow und auch Links auf
einzelne Fotos. Wer die Galerie schon freigeschaltet hat, bleibt freigeschaltet.

## Umzug von der Java-Fassung

Das Datenverzeichnis der alten Anwendung wird unverändert übernommen. Die Galerie liest daraus
`wedding.db` — nur lesend — für Reihenfolge, Originalnamen (im ZIP) und „Foto von …“, und verwendet die
schon erzeugten Vorschaubilder weiter. Nichts muss umbenannt oder neu berechnet werden.

```bash
# auf dem Server
mkdir -p /opt/docker-services/galerie && cd /opt/docker-services/galerie
git clone <repo> .                           # oder Dateien anders herüberbringen
cp .env.example .env                         # PHOTOS_TOKEN, PHOTOS_PASSWORD wie bisher; GALERIE_PORT=64711

cd /opt/docker-services/hochzeit && docker compose down     # alte Anwendung stoppen
cp -a data /opt/docker-services/galerie/data                # -a: Zeitstempel und Rechte bleiben

cd /opt/docker-services/galerie && docker compose up -d --build
docker compose logs galerie | head           # "Reihenfolge und Namen aus der alten Datenbank … photos=…"
```

Token und Passwort stehen bisher in der `docker-compose.yml` oder in `config/application.yml` der alten
Anwendung (`photos-token`, `photos-password`). **Beide müssen exakt gleich bleiben**, sonst funktionieren
QR-Codes und Freischaltungen nicht mehr. Ebenso die Domain: der Reverse Proxy zeigt weiter auf Port 64711.

Zurück geht es jederzeit: neuen Container stoppen, alten starten — dessen Daten wurden nicht angefasst.

## Fotos verwalten

- **Hinzufügen:** Datei in den Fotoordner `data/assets/photos/<Token>/` legen (SFTP, `scp`, …). Sie
  erscheint nach spätestens einer Minute — so lange gilt sie als womöglich noch in Übertragung — und
  zwar nach den Fotos aus der alten Datenbank, sortiert nach Dateinamen (`foto-2` vor `foto-10`). Die
  Vorschaubilder entstehen im Hintergrund.
- **Entfernen:** Datei löschen. Ihre Vorschaubilder in `thumbs/` und `large/` stören nicht.
- **Ohne `wedding.db`** (oder `LEGACY_DB=off`) sortiert die Galerie alles nach Dateinamen.

Formate: JPEG, PNG, GIF, WebP. HEIC/HEIF werden angezeigt, aber nicht verkleinert (Go kann sie ohne
C-Bibliotheken nicht lesen) — Galerie und Großansicht laden dann das Original.

`thumbs/` und `large/` lassen sich jederzeit löschen; die Fassungen entstehen dann neu (rund eine
Sekunde pro 24-MP-Foto, `IMAGE_WORKERS` parallel).

Wer nur die Domain aufruft, landet auf der Passwortseite der Galerie. Ohne `PHOTOS_PASSWORD` antwortet
`/` dagegen mit 404 — sonst stünde die Galerie jedem offen.

Den Link ohne Passworteingabe zeigt `docker compose exec galerie galerie link https://<domain>`.

## Einstellungen

Alle in [`.env.example`](.env.example). Die wichtigsten:

| Variable           | Vorgabe                                  |                                                    |
|--------------------|------------------------------------------|----------------------------------------------------|
| `PHOTOS_TOKEN`     | —                                        | Pflicht; geheimer Teil der Adresse                 |
| `PHOTOS_PASSWORD`  | leer                                     | leer = Galerie offen und kein ZIP-Download         |
| `COUPLE_NAME_1/2`  | Julia / Danny                            | Überschrift, Titel, Fußzeile                       |
| `GALLERY_SUBTITLE` | Die schönsten Momente unserer Hochzeit   |                                                    |
| `ZIP_NAME`         | Hochzeitsfotos.zip                       |                                                    |
| `DATA_DIR`         | `data` (Container: `/data`)              | aufgebaut wie das alte Datenverzeichnis            |
| `PHOTOS_DIR`       | `<DATA_DIR>/assets/photos/<Token>`       |                                                    |
| `CACHE_DIR`        | `<PHOTOS_DIR>` (darin `thumbs/`, `large/`) |                                                  |
| `LEGACY_DB`        | `<DATA_DIR>/wedding.db`                  | nur lesend; `off` = nur nach Dateinamen            |
| `IMAGE_WORKERS`    | 2                                        | gleichzeitig verkleinerte Fotos, je ~70 MB Speicher |

## Entwicklung

```bash
make help     # alle Ziele
make dev      # Server auf :8080 mit .env.local
make test     # go test ./...
make check    # go vet, gofmt
make css      # Tailwind: ui/frontend/app.css → ui/static/css/app.css
make docker   # Image hochzeit-galerie:latest
```

Das CSS liegt gebaut und **unminifiziert** im Repo, damit `go build` ohne Node auskommt. Nach neuen
Tailwind-Klassen in den Templates `make css` laufen lassen.

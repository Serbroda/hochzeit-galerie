# syntax=docker/dockerfile:1

# --- Version: das Build-Argument, sonst git describe des Checkouts (docker compose übergibt keins) ---
FROM docker.io/library/alpine:3.24 AS version
RUN apk add --no-cache git
ARG VERSION=
# Der Kontext ist nur für diesen Schritt eingebunden, .git landet nie in einem Image; ohne ist die Version "dev"
RUN --mount=type=bind,target=/src \
    echo "${VERSION:-$(git -c safe.directory='*' -C /src describe --tags --always 2>/dev/null || echo dev)}" > /version

# --- Server: ein statisches Binary mit Templates, CSS und JS eingebettet ------------------------------
# Das CSS liegt fertig im Repo (make css), deshalb braucht der Build kein Node.
FROM docker.io/library/golang:1.27-alpine AS server
WORKDIR /build
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd cmd
COPY internal internal
COPY ui ui
COPY --from=version /version /version
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(cat /version)" -o /galerie ./cmd/galerie

# --- Nur das Binary: baut bin/galerie ohne Go auf dem Rechner ----------------------------------------
#   docker build --target binary --output bin .
FROM scratch AS binary
COPY --from=server /galerie /galerie

# --- Laufzeit ------------------------------------------------------------------------------------------
FROM docker.io/library/alpine:3.24

# Feste IDs, damit sich ein eingebundenes Datenverzeichnis auch auf dem Host vorbereiten lässt (chown 1000:1000 data)
RUN apk add --no-cache su-exec tzdata \
    && addgroup -S -g 1000 galerie && adduser -S -u 1000 -G galerie galerie \
    && mkdir /data && chown galerie:galerie /data
COPY --from=server /galerie /usr/local/bin/galerie
COPY --chmod=755 docker/entrypoint.sh /entrypoint.sh

# /data ist aufgebaut wie das Datenverzeichnis der Java-Fassung und lässt sich von dort einfach übernehmen:
# wedding.db (nur gelesen) und assets/photos/<PHOTOS_TOKEN>/ mit den Fotos und thumbs/, large/
ENV ADDR=:8080 \
    DATA_DIR=/data \
    TZ=Europe/Berlin
VOLUME /data
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://localhost:8080/health || exit 1

# Startet nur als root, um die Rechte an /data zu richten, und läuft dann als Benutzer galerie
ENTRYPOINT ["/entrypoint.sh"]
CMD ["galerie"]

#!/bin/sh
set -e

# Eingebundene Verzeichnisse (./data:/data) behalten die Rechte des Hosts - von der Java-Fassung übernommene
# Dateien gehören meist root. Die Galerie schreibt ihre Vorschaubilder neben die Fotos (thumbs/, large/),
# braucht dort also Schreibrechte. Als root gestartet: /data übergeben und die Rechte abgeben.
if [ "$(id -u)" = "0" ]; then
	if ! find /data \! -user galerie -exec chown galerie:galerie {} + 2>/dev/null; then
		echo "WARNUNG: /data lässt sich nicht dem Benutzer galerie übergeben. Auf SELinux-Hosts (Fedora, RHEL)" >&2
		echo "         mit :Z einbinden, also ./data:/data:Z, oder auf dem Host 'chown -R 1000:1000 ./data'." >&2
	fi
	exec su-exec galerie "$@"
fi
exec "$@"

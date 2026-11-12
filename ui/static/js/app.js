document.addEventListener('alpine:init', () => {

    // Galerie: Kacheln im Mauerwerk und die Grossansicht zum Durchblaettern.
    Alpine.data('photoGallery', () => ({
        // Grossansicht
        items: [],
        index: 0,
        open: false,
        loaded: false,
        touchX: null,

        // Kacheln werden schrittweise angelegt: 600 auf einmal waeren Zehntausende DOM-Knoten.
        token: window.__photosToken || '',
        shown: 0,
        pageSize: 60,

        init() {
            const base = '/photos/' + this.token + '/';
            this.items = (window.__photos || []).map(p => {
                const name = encodeURIComponent(p.file);
                return {
                    file: p.file,
                    thumb: base + 'thumb/' + name,
                    large: base + 'large/' + name,
                    original: base + 'original/' + name,
                    by: p.by || '',
                };
            });
            // Zurueck-Taste (vor allem auf dem Handy) schliesst die Grossansicht statt die Seite zu verlassen.
            window.addEventListener('popstate', () => this.hide());

            this.observer = new ResizeObserver(() => this.scheduleLayout());
            this.observer.observe(this.$refs.tiles);
            // Grosszuegiger Vorlauf, damit beim Scrollen nie das Ende sichtbar wird.
            new IntersectionObserver(entries => {
                if (entries.some(e => e.isIntersecting)) this.showMore();
            }, { rootMargin: '0px 0px 1500px 0px' }).observe(this.$refs.more);
            this.showMore();
        },

        showMore() {
            if (this.shown < this.items.length) {
                this.shown = Math.min(this.items.length, this.shown + this.pageSize);
            }
        },

        // Aus x-init jeder neuen Kachel. Waechst sie (Foto geladen) oder aendert sich die Breite,
        // wird neu angeordnet.
        addTile(tile) {
            this.observer.observe(tile);
            this.scheduleLayout();
        },

        scheduleLayout() {
            if (this.layoutQueued) return;
            this.layoutQueued = true;
            requestAnimationFrame(() => {
                this.layoutQueued = false;
                this.layout();
            });
        },

        // Mauerwerk in Lesereihenfolge: jede Kachel kommt in die Spalte, die gerade am kuerzesten ist,
        // und wird absolut dorthin gesetzt. Kein CSS-Raster aus 2-px-Zeilen - bei 600 Fotos waeren das
        // Zehntausende Zeilen, Browser deckeln die Zahl, und dann stapelten sich die letzten Fotos.
        // Bis ein Foto geladen ist, haelt es einen 4:3-Platz frei; sonst laegen alle Kacheln bei 0 px
        // im sichtbaren Bereich und alle Fotos wuerden auf einmal geladen.
        layout() {
            const container = this.$refs.tiles;
            const tiles = [...container.children].filter(el => el.tagName === 'DIV');
            const style = getComputedStyle(container);
            const cols = parseInt(style.getPropertyValue('--cols')) || 2;
            const gap = parseFloat(style.getPropertyValue('--gap')) || 0;
            const width = (container.clientWidth - gap * (cols - 1)) / cols;
            // Erst alle Breiten schreiben, dann alle Hoehen lesen - sonst ein Reflow pro Kachel.
            tiles.forEach(tile => { tile.style.width = width + 'px'; });
            const heights = tiles.map(tile => tile.offsetHeight);
            const bottoms = new Array(cols).fill(0);
            tiles.forEach((tile, i) => {
                const col = bottoms.indexOf(Math.min(...bottoms));
                tile.style.transform = `translate(${col * (width + gap)}px, ${bottoms[col]}px)`;
                bottoms[col] += heights[i] + gap;
            });
            container.style.height = Math.max(0, Math.max(...bottoms) - gap) + 'px';
            // Der IntersectionObserver meldet sich nur bei Aenderungen. Liegt das Ende nach dem
            // Anhaengen noch immer im Vorlauf (grosser Bildschirm), gleich weitermachen.
            if (this.$refs.more.getBoundingClientRect().top < window.innerHeight + 1500) this.showMore();
        },

        get current() {
            return this.items[this.index] || {};
        },

        show(i) {
            if (!this.open) {
                history.pushState({ lightbox: true }, '');
                document.body.style.overflow = 'hidden';
                this.loaded = false; // das grosse Bild wird beim Oeffnen neu angelegt
            }
            this.go(i);
            this.open = true;
        },

        close() {
            if (history.state && history.state.lightbox) {
                history.back(); // loest popstate aus und damit hide()
            } else {
                this.hide();
            }
        },

        hide() {
            this.open = false;
            document.body.style.overflow = '';
        },

        go(i) {
            const n = this.items.length;
            if (n === 0) return;
            const next = (i + n) % n;
            if (next !== this.index) this.loaded = false;
            this.index = next;
            // Wer in der Grossansicht weiterblaettert, findet das Foto danach auch in der Galerie.
            while (this.shown <= next) this.showMore();
            // Nachbarn vorladen, damit das Weiterblaettern ohne Wartezeit geht.
            [next - 1, next + 1].forEach(j => {
                const item = this.items[(j + n) % n];
                if (item) new Image().src = item.large;
            });
        },

        prev() { this.go(this.index - 1); },
        next() { this.go(this.index + 1); },

        onKey(e) {
            if (!this.open) return;
            if (e.key === 'ArrowLeft') this.prev();
            else if (e.key === 'ArrowRight') this.next();
            else if (e.key === 'Escape') this.close();
        },

        touchStart(e) {
            this.touchX = e.changedTouches[0].clientX;
        },

        touchEnd(e) {
            if (this.touchX === null) return;
            const dx = e.changedTouches[0].clientX - this.touchX;
            this.touchX = null;
            if (Math.abs(dx) < 50) return;
            dx > 0 ? this.prev() : this.next();
        },
    }));
});

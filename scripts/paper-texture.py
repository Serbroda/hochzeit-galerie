#!/usr/bin/env python3
# Erzeugt die Papiertextur für die Fotoseiten (src/main/resources/static/img/paper.jpg):
# eine nahtlos kachelbare 512er-Kachel Naturpapier mit Fasern und Pünktchen. Fester Seed,
# damit ein erneuter Lauf dasselbe Bild liefert. Braucht Pillow.
#
#   python3 scripts/paper-texture.py src/main/resources/static/img/paper.jpg
import random, math, sys
from PIL import Image, ImageDraw, ImageFilter, ImageChops
random.seed(2026)
N = 512          # Kachelgröße
S = 2            # Supersampling für weiche Kanten
W = N * S
BASE = (242, 239, 235)

# 1) Leichte Wolkigkeit: grobes Zufallsraster, gekachelt hochskaliert -> nahtlos
g = 16
small = Image.new('L', (g, g))
small.putdata([random.randint(0, 255) for _ in range(g * g)])
tiled = Image.new('L', (g * 3, g * 3))
for i in range(3):
    for j in range(3):
        tiled.paste(small, (i * g, j * g))
big = tiled.resize((W * 3, W * 3), Image.BICUBIC).crop((W, W, 2 * W, 2 * W))
mottle = big.point(lambda v: 128 + (v - 128) * 2 // 128)  # ±2

img = Image.new('RGB', (W, W), BASE)
off = Image.merge('RGB', [mottle] * 3)
img = ImageChops.add(img, off, offset=-128)

# Zeichnet auf eine 3x3-Fläche, damit Elemente über den Rand nahtlos weiterlaufen
layer = Image.new('RGBA', (W * 3, W * 3), (0, 0, 0, 0))
d = ImageDraw.Draw(layer)

def tint():
    # Strohig-beige bis graubraun, wie Fasern im Naturpapier
    t = random.random()
    if t < 0.55:   v = random.randint(150, 195); return (v, v - 6, v - 22)
    if t < 0.85:   v = random.randint(120, 170); return (v, v - 4, v - 8)
    v = random.randint(170, 200); return (v - 10, v - 2, v - 30)  # leicht grünlich

def everywhere(fn):
    x, y = random.uniform(0, W), random.uniform(0, W)
    for ox in (0, W, 2 * W):
        for oy in (0, W, 2 * W):
            fn(x + ox, y + oy)

# 2) Fasern: kurze, leicht gebogene Striche
for _ in range(230):
    length = random.uniform(3, 15) * S
    angle = random.uniform(0, math.pi)
    bend = random.uniform(-1.4, 1.4)
    col = tint() + (random.randint(60, 160),)
    width = 1 if random.random() < 0.85 else 2
    def fiber(x, y):
        pts = []
        a = angle
        for k in range(8):
            pts.append((x, y))
            x += math.cos(a) * length / 7
            y += math.sin(a) * length / 7
            a += bend / 7
        d.line(pts, fill=col, width=width)
    everywhere(fiber)

# 3) Pünktchen
for _ in range(900):
    r = random.choice([0.3, 0.35, 0.4, 0.5, 0.5, 0.6, 0.8, 1.1]) * S
    col = tint() + (random.randint(50, 170),)
    everywhere(lambda x, y: d.ellipse((x - r, y - r, x + r, y + r), fill=col))

# ein paar hellere Einschlüsse
for _ in range(40):
    r = random.uniform(0.6, 1.4) * S
    everywhere(lambda x, y: d.ellipse((x - r, y - r, x + r, y + r), fill=(250, 248, 244, 110)))

layer = layer.crop((W, W, 2 * W, 2 * W))
img = img.convert('RGBA')
img.alpha_composite(layer)
img = img.convert('RGB').resize((N, N), Image.LANCZOS)

# 4) Feines Korn
noise = Image.effect_noise((N, N), 6).point(lambda v: 128 + (v - 128) // 3)
img = ImageChops.add(img, Image.merge('RGB', [noise] * 3), offset=-128)

img.save(sys.argv[1], quality=82, optimize=True, progressive=True)

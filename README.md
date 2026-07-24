# Gala

**Gala** is an incremental static gallery generator for directory trees.

It reads an originals directory, creates a separate static website with square
thumbnails and medium previews, and links directly back to the original files.
The source tree is never modified.

## Version 0.1.1

This first version provides:

- one generated `index.html` per source directory;
- stable folder covers chosen from the first usable descendant image;
- visibly different folder cards;
- square, center-cropped thumbnails;
- previews with a maximum dimension of 1920 pixels by default;
- a compact breadcrumb heading without a separate branded header;
- a keyboard, mouse, and touch lightbox that contains the complete image without cropping;
- left/right navigation by clicking the corresponding side of the lightbox;
- a bottom click area that reveals the filename and original download link;
- direct links for all non-image files;
- parallel image processing;
- an incremental manifest, so unchanged images are not regenerated;
- deletion of stale generated pages and media assets;
- a lock file to prevent overlapping cron builds;
- JPEG EXIF-orientation correction;
- no runtime dependencies beyond Go for building the executable.

Native preview generation currently supports JPEG, PNG, and the first frame of
GIF files. Other files, including RAW, PDF, video, text, archives, and unknown
formats, are listed and linked to their originals but do not yet receive visual
previews. Those handlers are intended for the next version.

## Build

Go 1.22 or newer:

```sh
go build -o gala .
```

Install for the current user:

```sh
install -Dm755 gala "$HOME/.local/bin/gala"
```

## Use

The normal invocation needs only the originals directory:

```sh
gala ./originals
```

This creates `./gala-site`:

```text
originals/                 original files, untouched
gala-site/
├── index.html
├── subfolder/index.html
├── _gala/
│   ├── gala.css
│   ├── gala.js
│   ├── thumbs/
│   └── previews/
└── .gala-manifest.json
```

Choose another output directory with a second positional argument:

```sh
gala ./originals ./website
```

or:

```sh
gala ./originals --output ./website
```

When source and output are visible under the same web root, Gala calculates
relative links to the originals automatically. For a separate URL mapping,
provide the public prefix:

```sh
gala /srv/photos /var/www/gallery --original-url /photos/
```

An original at `/srv/photos/trips/alps.jpg` then receives the URL:

```text
/photos/trips/alps.jpg
```

## Useful options

```text
-j, --workers N        parallel image workers
--thumb-size N         square thumbnail dimension; default 320
--preview-size N       maximum preview dimension; default 1920
--original-url URL     public URL prefix for originals
--force                regenerate all previews
--dry-run              scan and report without writing
```

## Cron

Absolute paths are recommended under cron:

```cron
*/5 * * * * /home/b/.local/bin/gala /var/www/originals /var/www/gala-site >>/var/log/gala.log 2>&1
```

Each run scans the complete directory tree but regenerates expensive image
assets only when the source size or modification time has changed. HTML pages
are inexpensive and are rebuilt each time.

## Serving locally

From the directory containing both `originals` and `gala-site`:

```sh
python3 -m http.server 8000
```

Open:

```text
http://localhost:8000/gala-site/
```

## Safety

Gala rejects an output directory located inside the source tree. Generated
content remains separate, and the originals directory is opened read-only.

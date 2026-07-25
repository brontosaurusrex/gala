# Gala

**Gala** is an incremental static gallery generator for directory trees.

It reads an originals directory, creates a separate static website with square
thumbnails and medium previews, and links directly back to the original files.
The source tree is never modified.

This file is named `READMEAI.md` so you can keep your own `README.md` beside it.

## Version 0.1.12

This version provides:

- smaller selectable captions beneath media thumbnails;
- the Inter variable WOFF2 webfont embedded into the Gala binary and generated site;

- one generated `index.html` per source directory;
- stable folder covers chosen from the first usable descendant image or RAW photo;
- square, center-cropped thumbnails and uncropped previews up to 1920 pixels;
- native previews for JPEG, PNG, and the first frame of GIF files;
- MP4/MOV/M4V/WebM/MKV thumbnails and poster frames through `ffmpeg`;
- native browser video playback using the original video file;
- first-page PDF previews through `pdftocairo`;
- RAW preview support for CR2, CR3, NEF, ARW, RAF, DNG, ORF, RW2, PEF,
  SRW, X3F, 3FR, IIQ, and other common RAW extensions;
- RAW fallback order: ExifTool embedded image, dcraw embedded image, then
  ImageMagick rendering;
- a virtual `Videos` collection at the project root whenever videos are present;
- a purple collection/play overlay that distinguishes virtual folders from real folders;
- centered breadcrumb headings without a branded header or divider line;
- a keyboard, mouse, and touch lightbox that fits complete images without cropping;
- hover hints for the lightbox exit and download areas;
- deep links for individual lightbox items using `#media=...`, plus a Copy link button;
- selected EXIF details shown in the lightbox when ExifTool metadata is available;
- generated preview URLs include the original base filename as an `filename=...` query parameter;
- missing EXIF fields remain empty instead of rendering as `<nil>`;
- previous/next controls that disappear at the ends, with no wrap-around;
- a footer credit that stays hidden until the footer is hovered;
- parallel and incremental media processing;
- cache-busting URLs and stale generated-file cleanup;
- a PID-based build lock that is removed on Ctrl+C or SIGTERM;
- automatic recovery from lock files belonging to dead processes;
- one clear dependency warning per affected media type instead of one warning per file;
- JPEG EXIF-orientation correction.


## Bundled Inter font

Before building, place the normal Inter variable webfont at:

```text
assets/InterVariable.woff2
```

Gala embeds this file into the executable. Each generated site receives:

```text
gala-site/_gala/InterVariable.woff2
```

The generated CSS uses Inter first, followed by Segoe UI and the operating
system's normal interface font. The gallery therefore works offline and through
`file://` without requiring Inter to be installed on the viewer's computer.
Retain Inter's SIL Open Font License when redistributing the font or a Gala
binary containing it.

## Build

Go 1.22 or newer:

```sh
go build -o gala .
```

Install for the current user:

```sh
install -Dm755 gala "$HOME/.local/bin/gala"
```

## Optional preview helpers

Normal JPEG, PNG, and GIF images need no external runtime dependency.

```sh
sudo apt install ffmpeg poppler-utils libimage-exiftool-perl dcraw imagemagick
```

The helpers are used as follows:

```text
ffmpeg       video thumbnails and poster frames
pdftocairo   first-page PDF previews
exiftool     preferred embedded RAW preview extraction
dcraw        embedded RAW preview fallback
magick       full RAW rendering fallback, when supported by ImageMagick
```

You do not need every RAW helper. ExifTool alone handles many cameras because
most RAW files contain an embedded JPEG. If a required helper is absent, Gala
prints one warning for that media type, leaves those files as direct downloads,
and continues building the rest of the site.

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
│   ├── collections/videos/index.html
│   ├── thumbs/
│   └── previews/
├── .gala-manifest.json
└── .gala.lock             exists only while a build is running
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
relative links to the originals automatically. For a separate URL mapping:

```sh
gala /srv/photos /var/www/gallery --original-url /photos/
```

## Useful options

```text
-j, --workers N        parallel media workers; default up to 4
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

Each run scans the directory tree but regenerates expensive media assets only
when the source size, modification time, or output settings changed.

## Serving locally

From the directory containing both `originals` and `gala-site`:

```sh
python3 -m http.server 8000
```

Then open:

```text
http://localhost:8000/gala-site/
```

## Safety

Gala rejects an output directory located inside the source tree. Generated
content remains separate, and originals are only opened for reading. Ctrl+C and
SIGTERM cancel active helper processes and remove the build lock. A hard kill
such as `kill -9` cannot run cleanup, so Gala checks the stored PID and removes
the stale lock on the next run when that process no longer exists.

- Media captions are outside the lightbox link, so filenames and dimensions can be selected normally.

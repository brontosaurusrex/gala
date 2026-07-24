# Gala

**Gala** is an incremental static gallery generator for directory trees.

It reads an originals directory, creates a separate static website with square
thumbnails and medium previews, and links directly back to the original files.
The source tree is never modified.

![slopware warning](https://brontosaurusrex.github.io/media/slopware02.svg)

## Version 0.1.4

This version provides:

- one generated `index.html` per source directory;
- stable folder covers chosen from the first usable descendant image;
- folder cards identified by a folder icon without a special border;
- square, center-cropped thumbnails;
- previews with a maximum dimension of 1920 pixels by default;
- native previews for JPEG, PNG, and the first frame of GIF files;
- MP4/MOV/M4V/WebM/MKV thumbnails through `ffmpeg`, marked by a simple play icon;
- uncropped video poster frames up to 1920 pixels, shown by the native video
  player before playback begins;
- native browser video playback in the lightbox using the original file;
- a virtual `Videos` folder at the project root whenever videos are present;
- first-page PDF thumbnails through `pdftocairo`;
- centered breadcrumb headings without a separate branded page header or divider line;
- a keyboard, mouse, and touch lightbox that fits the complete preview without cropping;
- lightbox navigation by clicking the left or right side;
- clicking the top area of the lightbox closes it;
- previous/next controls disappear at the first and last item, with no wrap-around;
- a bottom click area that reveals the filename and original download link;
- a footer credit that stays hidden until the footer is hovered;
- direct links for files without previews;
- parallel media processing;
- an incremental manifest, so unchanged previews are not regenerated;
- cache-busting URLs for generated CSS, JavaScript, thumbnails, and previews;
- deletion of stale generated pages and media assets;
- a lock file to prevent overlapping cron builds;
- JPEG EXIF-orientation correction.

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

Normal images need no external runtime dependency. Install these to generate
video and PDF previews:

```sh
sudo apt install ffmpeg poppler-utils
```

When either helper is absent or a file cannot be rendered, Gala keeps that file
as an ordinary direct link and continues building the rest of the site.

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
-j, --workers N        parallel media workers
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

Each run scans the complete directory tree but regenerates expensive media
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

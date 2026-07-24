package main

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEscapeRelURL(t *testing.T) {
	got := escapeRelURL("../originals/My photo #1.jpg")
	want := "../originals/My%20photo%20%231.jpg"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		999:         "999 B",
		1024:        "1.0 KiB",
		1024 * 1024: "1.0 MiB",
	}
	for input, want := range cases {
		if got := humanSize(input); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", input, got, want)
		}
	}
}

func TestSquareThumbnail(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 400, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 400; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: uint8(x % 256), G: uint8(y), A: 255})
		}
	}
	thumb := makeSquareThumb(src, 64)
	if thumb.Bounds().Dx() != 64 || thumb.Bounds().Dy() != 64 {
		t.Fatalf("thumbnail bounds = %v", thumb.Bounds())
	}
}

func TestAcquireLock(t *testing.T) {
	dir := t.TempDir()
	release, err := acquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireLock(dir); err == nil {
		t.Fatal("second lock unexpectedly succeeded")
	}
	release()
	if _, err := os.Stat(filepath.Join(dir, ".gala.lock")); !os.IsNotExist(err) {
		t.Fatalf("lock remains after release: %v", err)
	}
}

func TestDefaultPreviewSize(t *testing.T) {
	opts, err := parseArgs([]string{"./originals"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.PreviewSize != 1920 {
		t.Fatalf("default preview size = %d, want 1920", opts.PreviewSize)
	}
}

func TestPortraitPreviewFitsWithinMaximum(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 1000, 3000))
	preview := makePreview(src, 1920)
	if preview.Bounds().Dx() != 640 || preview.Bounds().Dy() != 1920 {
		t.Fatalf("preview bounds = %v, want 640x1920", preview.Bounds())
	}
}

func TestScanTreeClassifiesPreviewableMedia(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"photo.jpg", "movie.mp4", "paper.pdf", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, files, err := scanTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]ScanFile)
	for _, f := range files {
		got[f.Name] = f
	}
	cases := []struct {
		name        string
		kind        string
		previewable bool
	}{
		{"photo.jpg", "image", true},
		{"movie.mp4", "video", true},
		{"paper.pdf", "pdf", true},
		{"notes.txt", "file", false},
	}
	for _, tc := range cases {
		f := got[tc.name]
		if f.Kind != tc.kind || f.IsPreviewable != tc.previewable {
			t.Errorf("%s: kind=%q previewable=%v, want %q %v", tc.name, f.Kind, f.IsPreviewable, tc.kind, tc.previewable)
		}
	}
}

func TestWithVersion(t *testing.T) {
	if got := withVersion("../_gala/gala.css", "0.1.4"); got != "../_gala/gala.css?v=0.1.4" {
		t.Fatalf("unexpected versioned URL %q", got)
	}
	if got := withVersion("image.jpg?x=1", "two words"); got != "image.jpg?x=1&v=two+words" {
		t.Fatalf("unexpected versioned URL with query %q", got)
	}
}

func TestCacheTokenIncludesOutputSizes(t *testing.T) {
	entry := ManifestImage{Size: 123, ModNano: 456}
	a := cacheToken(entry, Options{ThumbSize: 320, PreviewSize: 1920})
	b := cacheToken(entry, Options{ThumbSize: 320, PreviewSize: 1000})
	if a == b {
		t.Fatalf("cache token did not change with preview size: %q", a)
	}
}

func TestLightboxDoesNotWrap(t *testing.T) {
	checks := []string{
		"index = Math.max(0, Math.min(cards.length - 1, i))",
		"previousButton.hidden = !hasMultiple || index === 0",
		"nextButton.hidden = !hasMultiple || index === cards.length - 1",
	}
	for _, check := range checks {
		if !strings.Contains(galaJS, check) {
			t.Fatalf("galaJS missing %q", check)
		}
	}
}

func TestVideoUsesOriginalWithGeneratedPoster(t *testing.T) {
	checks := []string{
		`<video class="lightbox-video" controls preload="metadata" hidden></video>`,
		`data-kind="{{.Kind}}"`,
		`data-poster="{{.PosterURL}}"`,
		`video.poster = card.dataset.poster || ''`,
		`video.src = card.dataset.original`,
		`if (currentKind === 'video' && event.target === video) return`,
		`document.activeElement === video`,
	}
	for _, check := range checks {
		if !strings.Contains(pageTemplate+galaJS, check) {
			t.Fatalf("video lightbox implementation missing %q", check)
		}
	}
}

func TestVideoCacheRequiresThumbnailAndPoster(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"thumb.jpg", "poster.jpg"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	complete := ManifestImage{Thumb: "thumb.jpg", Preview: "poster.jpg"}
	if !manifestAssetsExist(dir, "video", complete) {
		t.Fatal("video entry should require both thumbnail and poster")
	}
	missingPoster := ManifestImage{Thumb: "thumb.jpg"}
	if manifestAssetsExist(dir, "video", missingPoster) {
		t.Fatal("video entry without a poster should be regenerated")
	}
}

func TestFooterCreditIsHoverOnly(t *testing.T) {
	checks := []string{
		`<footer><span>Generated by Gala`,
		`footer span { opacity: 0;`,
		`footer:hover span { opacity: 1;`,
	}
	for _, check := range checks {
		if !strings.Contains(pageTemplate+galaCSS, check) {
			t.Fatalf("hover-only footer implementation missing %q", check)
		}
	}
}

func TestFolderAndVideoCardsUseIconsWithoutSpecialBorders(t *testing.T) {
	for _, unwanted := range []string{
		`.folder-card { border-color:`,
		`.folder-thumb::after`,
	} {
		if strings.Contains(galaCSS, unwanted) {
			t.Fatalf("galaCSS still contains special folder border styling %q", unwanted)
		}
	}
	checks := []string{
		`.folder-badge`,
		`.video .media-badge { min-width: auto;`,
		`border: 0; border-radius: 0; background: transparent;`,
	}
	for _, check := range checks {
		if !strings.Contains(galaCSS, check) {
			t.Fatalf("icon-only folder/video styling missing %q", check)
		}
	}
}

func TestHeaderHasNoDivider(t *testing.T) {
	if strings.Contains(galaCSS, ".site-header") && strings.Contains(galaCSS, "border-bottom: 1px solid #ffffff12") {
		t.Fatal("breadcrumb header still has a bottom divider")
	}
}

func TestVirtualVideosCollection(t *testing.T) {
	source := filepath.Join(t.TempDir(), "originals")
	output := filepath.Join(t.TempDir(), "site")
	if err := os.MkdirAll(filepath.Join(source, "clips"), 0o755); err != nil {
		t.Fatal(err)
	}
	videoPath := filepath.Join(source, "clips", "sample.mp4")
	if err := os.WriteFile(videoPath, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirs, files, err := scanTree(source)
	if err != nil {
		t.Fatal(err)
	}
	entry := ManifestImage{
		Size: 5, ModNano: 1,
		Thumb:   "_gala/thumbs/video.jpg",
		Preview: "_gala/previews/video.jpg",
		Width:   1920, Height: 1080,
	}
	images := map[string]ManifestImage{"clips/sample.mp4": entry}
	pages, err := writePages(source, output, Options{ThumbSize: 320, PreviewSize: 1920}, dirs, files, images, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 3 {
		t.Fatalf("generated %d pages, want root, clips, and Videos collection", len(pages))
	}

	rootHTML, err := os.ReadFile(filepath.Join(output, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rootHTML), `href="_gala/collections/videos/index.html"`) {
		t.Fatal("root page does not link to the virtual Videos collection")
	}

	collectionHTML, err := os.ReadFile(filepath.Join(output, "_gala", "collections", "videos", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	collection := string(collectionHTML)
	for _, check := range []string{
		`clips/sample.mp4`,
		`data-kind="video"`,
		`data-poster=`,
	} {
		if !strings.Contains(collection, check) {
			t.Fatalf("Videos collection missing %q", check)
		}
	}
}

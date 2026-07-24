package main

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
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

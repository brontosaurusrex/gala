package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"math"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const version = "0.1.9"

var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
}

var videoExtensions = map[string]bool{
	".mp4": true, ".m4v": true, ".mov": true, ".webm": true, ".mkv": true,
}

var rawExtensions = map[string]bool{
	".3fr": true, ".arw": true, ".cr2": true, ".cr3": true,
	".dng": true, ".erf": true, ".fff": true, ".iiq": true,
	".kdc": true, ".mef": true, ".mos": true, ".mrw": true,
	".nef": true, ".nrw": true, ".orf": true, ".pef": true,
	".raf": true, ".raw": true, ".rwl": true, ".rw2": true,
	".sr2": true, ".srf": true, ".srw": true, ".x3f": true,
}

type Options struct {
	Source      string
	Output      string
	OriginalURL string
	Workers     int
	ThumbSize   int
	PreviewSize int
	Force       bool
	DryRun      bool
}

type ScanFile struct {
	Rel           string
	Abs           string
	DirRel        string
	Name          string
	Ext           string
	Size          int64
	ModNano       int64
	IsImage       bool
	IsPreviewable bool
	Kind          string
}

type Manifest struct {
	Version     int                      `json:"version"`
	Source      string                   `json:"source"`
	ThumbSize   int                      `json:"thumb_size"`
	PreviewSize int                      `json:"preview_size"`
	UpdatedAt   string                   `json:"updated_at"`
	Images      map[string]ManifestImage `json:"images"`
	Pages       []string                 `json:"pages"`
}

type ManifestImage struct {
	Size       int64     `json:"size"`
	ModNano    int64     `json:"mtime_ns"`
	Thumb      string    `json:"thumb"`
	Preview    string    `json:"preview"`
	Width      int       `json:"width"`
	Height     int       `json:"height"`
	SourceType string    `json:"source_type"`
	ExifReady  bool      `json:"exif_ready"`
	Exif       *ExifInfo `json:"exif,omitempty"`
}

type imageJob struct {
	File ScanFile
	Old  ManifestImage
}

type imageResult struct {
	Rel   string
	Entry ManifestImage
	Err   error
}

type dirData struct {
	Rel     string
	Folders []string
	Files   []ScanFile
}

type Link struct {
	Label string
	Href  string
}

type FolderCard struct {
	Name     string
	Href     string
	ThumbURL string
	HasThumb bool
	Virtual  bool
	Count    int
}

type Dependencies struct {
	FFmpeg     string
	PDFToCairo string
	ExifTool   string
	DCRaw      string
	Magick     string
}

type ExifInfo struct {
	Camera               string `json:"camera,omitempty"`
	Lens                 string `json:"lens,omitempty"`
	CaptureTime          string `json:"captureTime,omitempty"`
	ExposureTime         string `json:"exposureTime,omitempty"`
	Aperture             string `json:"aperture,omitempty"`
	ISO                  string `json:"iso,omitempty"`
	FocalLength          string `json:"focalLength,omitempty"`
	ExposureCompensation string `json:"exposureCompensation,omitempty"`
}

type ImageCard struct {
	Name        string
	FileName    string
	MediaID     string
	ThumbURL    string
	PreviewURL  string
	PosterURL   string
	OriginalURL string
	ExifJSON    string
	Width       int
	Height      int
	Kind        string
	Badge       string
}

type FileCard struct {
	Name        string
	OriginalURL string
	Extension   string
	Size        string
}

type PageData struct {
	Title       string
	SourceName  string
	CSSURL      string
	JSURL       string
	Breadcrumbs []Link
	Folders     []FolderCard
	Images      []ImageCard
	Files       []FileCard
	Empty       bool
	GeneratedAt string
}

func main() {
	os.Exit(realMain())
}

func realMain() int {
	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "gala:", err)
		fmt.Fprintln(os.Stderr, "Try 'gala --help'.")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runContext(ctx, opts); err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "Gala: interrupted; temporary state and build lock cleaned up")
			return 130
		}
		fmt.Fprintln(os.Stderr, "gala:", err)
		return 1
	}
	return 0
}

func parseArgs(args []string) (Options, error) {
	workers := runtime.NumCPU()
	if workers > 4 {
		workers = 4
	}
	if workers < 1 {
		workers = 1
	}
	opts := Options{Output: "gala-site", Workers: workers, ThumbSize: 320, PreviewSize: 1920}
	var positional []string

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-h", "--help":
			printHelp()
			os.Exit(0)
		case "--version":
			fmt.Println("gala", version)
			os.Exit(0)
		case "-o", "--output":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("%s needs a directory", a)
			}
			opts.Output = args[i]
		case "-j", "--workers":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("%s needs a number", a)
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 {
				return opts, fmt.Errorf("invalid worker count %q", args[i])
			}
			opts.Workers = n
		case "--thumb-size":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("%s needs a number", a)
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 64 {
				return opts, fmt.Errorf("invalid thumbnail size %q", args[i])
			}
			opts.ThumbSize = n
		case "--preview-size":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("%s needs a number", a)
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 128 {
				return opts, fmt.Errorf("invalid preview size %q", args[i])
			}
			opts.PreviewSize = n
		case "--original-url":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("%s needs a URL prefix", a)
			}
			opts.OriginalURL = args[i]
		case "--force":
			opts.Force = true
		case "--dry-run":
			opts.DryRun = true
		default:
			if strings.HasPrefix(a, "-") {
				return opts, fmt.Errorf("unknown option %q", a)
			}
			positional = append(positional, a)
		}
	}

	if len(positional) == 0 {
		return opts, errors.New("missing source directory")
	}
	if len(positional) > 2 {
		return opts, errors.New("usage: gala SOURCE [OUTPUT]")
	}
	opts.Source = positional[0]
	if len(positional) == 2 {
		opts.Output = positional[1]
	}
	return opts, nil
}

func printHelp() {
	fmt.Print(`Gala — incremental static gallery generator.

Usage:
  gala SOURCE [OUTPUT]

Examples:
  gala ./originals
  gala ./originals ./website
  gala /var/www/originals /var/www/gallery --original-url /originals/

Options:
  -o, --output DIR       Output directory (default: ./gala-site)
  -j, --workers N        Parallel media workers (default: up to 4)
      --thumb-size N     Square thumbnail size (default: 320)
      --preview-size N   Maximum preview dimension (default: 1920)
      --original-url URL Public URL prefix for originals
      --force            Regenerate every thumbnail and preview
      --dry-run          Scan and report without writing
      --version          Show version
  -h, --help             Show help

The source tree is never modified. If --original-url is omitted, Gala creates
relative links from generated pages to the source tree.
`)
}

func run(opts Options) error {
	return runContext(context.Background(), opts)
}

func runContext(ctx context.Context, opts Options) error {
	source, err := filepath.Abs(opts.Source)
	if err != nil {
		return err
	}
	output, err := filepath.Abs(opts.Output)
	if err != nil {
		return err
	}
	st, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	if !st.IsDir() {
		return fmt.Errorf("source is not a directory: %s", source)
	}
	if sameOrInside(output, source) {
		return errors.New("output directory must not be inside the source tree")
	}
	if source == output {
		return errors.New("source and output directories must be different")
	}

	dirs, files, err := scanTreeContext(ctx, source)
	if err != nil {
		return err
	}
	deps := detectDependencies()

	manifestPath := filepath.Join(output, ".gala-manifest.json")
	oldManifest := loadManifest(manifestPath)
	if oldManifest.Images == nil {
		oldManifest.Images = make(map[string]ManifestImage)
	}

	previewFiles := make([]ScanFile, 0)
	for _, f := range files {
		if f.IsPreviewable {
			previewFiles = append(previewFiles, f)
		}
	}

	jobs := make([]imageJob, 0)
	newImages := make(map[string]ManifestImage, len(previewFiles))
	skippedByDependency := make(map[string]int)
	for _, f := range previewFiles {
		old, ok := oldManifest.Images[f.Rel]
		cacheCompatible := oldManifest.ThumbSize == opts.ThumbSize &&
			oldManifest.PreviewSize == opts.PreviewSize
		wantsExif := deps.ExifTool != "" && (f.Kind == "image" || f.Kind == "raw")
		unchanged := cacheCompatible && ok && old.Size == f.Size && old.ModNano == f.ModNano &&
			manifestAssetsExist(output, f.Kind, old) && (!wantsExif || old.ExifReady)
		if unchanged && !opts.Force {
			newImages[f.Rel] = old
		} else if !deps.canPreview(f.Kind) {
			skippedByDependency[f.Kind]++
		} else {
			jobs = append(jobs, imageJob{File: f, Old: old})
		}
	}
	reportMissingDependencies(skippedByDependency, deps)

	fmt.Printf("Gala: %d directories, %d files, %d previewable media, %d item%s to process\n",
		len(dirs), len(files), len(previewFiles), len(jobs), plural(len(jobs)))
	if opts.DryRun {
		fmt.Printf("Dry run: output would be written to %s\n", output)
		return nil
	}

	releaseLock, err := acquireLock(output)
	if err != nil {
		return err
	}
	defer releaseLock()

	if err := os.MkdirAll(filepath.Join(output, "_gala", "thumbs"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(output, "_gala", "previews"), 0o755); err != nil {
		return err
	}

	results := processImages(ctx, jobs, output, opts, deps)
	failures := 0
	for r := range results {
		if r.Err != nil {
			if errors.Is(r.Err, context.Canceled) && ctx.Err() != nil {
				continue
			}
			failures++
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", r.Rel, r.Err)
			continue
		}
		newImages[r.Rel] = r.Entry
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := writeStaticAssets(output); err != nil {
		return err
	}

	folderCover := chooseFolderCovers(dirs, files, newImages)
	if err := ctx.Err(); err != nil {
		return err
	}
	pages, err := writePages(source, output, opts, dirs, files, newImages, folderCover)
	if err != nil {
		return err
	}

	cleanupStale(output, oldManifest, newImages, pages)

	manifest := Manifest{
		Version:     7,
		Source:      source,
		ThumbSize:   opts.ThumbSize,
		PreviewSize: opts.PreviewSize,
		UpdatedAt:   time.Now().Format(time.RFC3339),
		Images:      newImages,
		Pages:       pages,
	}
	if err := writeJSONAtomic(manifestPath, manifest); err != nil {
		return err
	}

	fmt.Printf("Built %d page%s in %s", len(pages), plural(len(pages)), output)
	if failures > 0 {
		fmt.Printf(" (%d media warning%s)", failures, plural(failures))
	}
	fmt.Println()
	return nil
}

func acquireLock(output string) (func(), error) {
	if err := os.MkdirAll(output, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(output, ".gala.lock")
	for attempt := 0; attempt < 3; attempt++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			_, _ = fmt.Fprintf(f, "pid=%d\nstarted=%s\n", os.Getpid(), time.Now().Format(time.RFC3339))
			if err := f.Close(); err != nil {
				_ = os.Remove(path)
				return nil, err
			}
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}

		pid, readErr := lockPID(path)
		if readErr == nil && processAlive(pid) {
			return nil, fmt.Errorf("another Gala build is running with PID %d", pid)
		}
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return nil, fmt.Errorf("remove stale lock %s: %w", path, removeErr)
		}
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "warning: removed unreadable stale lock %s\n", path)
		} else {
			fmt.Fprintf(os.Stderr, "warning: removed stale Gala lock for PID %d\n", pid)
		}
	}
	return nil, fmt.Errorf("could not acquire build lock %s", path)
}

func lockPID(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "pid=") {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "pid=")))
		if err != nil || pid < 1 {
			return 0, errors.New("invalid PID in lock file")
		}
		return pid, nil
	}
	return 0, errors.New("lock file has no PID")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func detectDependencies() Dependencies {
	return Dependencies{
		FFmpeg:     findCommand("ffmpeg"),
		PDFToCairo: findCommand("pdftocairo"),
		ExifTool:   findCommand("exiftool"),
		DCRaw:      findCommand("dcraw"),
		Magick:     findCommand("magick", "convert"),
	}
}

func findCommand(names ...string) string {
	for _, name := range names {
		path, err := exec.LookPath(name)
		if err == nil {
			return path
		}
	}
	return ""
}

func (d Dependencies) canPreview(kind string) bool {
	switch kind {
	case "image":
		return true
	case "video":
		return d.FFmpeg != ""
	case "pdf":
		return d.PDFToCairo != ""
	case "raw":
		return d.ExifTool != "" || d.DCRaw != "" || d.Magick != ""
	default:
		return false
	}
}

func reportMissingDependencies(skipped map[string]int, deps Dependencies) {
	if n := skipped["video"]; n > 0 {
		fmt.Fprintf(os.Stderr, "warning: ffmpeg not found; %d video file%s will be shown as direct download%s\n", n, plural(n), plural(n))
	}
	if n := skipped["pdf"]; n > 0 {
		fmt.Fprintf(os.Stderr, "warning: pdftocairo not found; %d PDF file%s will be shown as direct download%s\n", n, plural(n), plural(n))
	}
	if n := skipped["raw"]; n > 0 {
		fmt.Fprintf(os.Stderr, "warning: no RAW preview helper found; install exiftool, dcraw, or ImageMagick (%d RAW file%s shown as direct download%s)\n", n, plural(n), plural(n))
	}
}

func sameOrInside(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func scanTree(source string) (map[string]*dirData, []ScanFile, error) {
	return scanTreeContext(context.Background(), source)
}

func scanTreeContext(ctx context.Context, source string) (map[string]*dirData, []ScanFile, error) {
	dirs := map[string]*dirData{"": {Rel: ""}}
	var files []ScanFile

	err := filepath.WalkDir(source, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		relOS, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(relOS)
		name := d.Name()

		if d.IsDir() {
			if name == ".git" || name == ".gala" {
				return filepath.SkipDir
			}
			dirs[rel] = &dirData{Rel: rel}
			parent := filepath.ToSlash(filepath.Dir(relOS))
			if parent == "." {
				parent = ""
			}
			dirs[parent].Folders = append(dirs[parent].Folders, rel)
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		dirRel := filepath.ToSlash(filepath.Dir(relOS))
		if dirRel == "." {
			dirRel = ""
		}
		ext := strings.ToLower(filepath.Ext(name))
		kind := "file"
		isImage := imageExtensions[ext]
		isPreviewable := isImage
		if isImage {
			kind = "image"
		} else if videoExtensions[ext] {
			kind = "video"
			isPreviewable = true
		} else if ext == ".pdf" {
			kind = "pdf"
			isPreviewable = true
		} else if rawExtensions[ext] {
			kind = "raw"
			isPreviewable = true
		}
		f := ScanFile{
			Rel: rel, Abs: path, DirRel: dirRel, Name: name, Ext: ext,
			Size: info.Size(), ModNano: info.ModTime().UnixNano(),
			IsImage: isImage, IsPreviewable: isPreviewable, Kind: kind,
		}
		files = append(files, f)
		dirs[dirRel].Files = append(dirs[dirRel].Files, f)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	for _, d := range dirs {
		sort.Slice(d.Folders, func(i, j int) bool {
			return strings.ToLower(filepath.Base(d.Folders[i])) < strings.ToLower(filepath.Base(d.Folders[j]))
		})
		sort.Slice(d.Files, func(i, j int) bool {
			return strings.ToLower(d.Files[i].Name) < strings.ToLower(d.Files[j].Name)
		})
	}
	sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Rel) < strings.ToLower(files[j].Rel) })
	return dirs, files, nil
}

func loadManifest(path string) Manifest {
	var m Manifest
	b, err := os.ReadFile(path)
	if err != nil {
		return m
	}
	if json.Unmarshal(b, &m) != nil {
		return Manifest{}
	}
	return m
}

func processImages(ctx context.Context, jobs []imageJob, output string, opts Options, deps Dependencies) <-chan imageResult {
	jobCh := make(chan imageJob)
	resultCh := make(chan imageResult)
	var wg sync.WaitGroup

	workers := opts.Workers
	if workers > len(jobs) && len(jobs) > 0 {
		workers = len(jobs)
	}
	if workers < 1 {
		workers = 1
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case job, ok := <-jobCh:
					if !ok {
						return
					}
					entry, err := generateImageAssets(ctx, job.File, output, opts, deps)
					select {
					case resultCh <- imageResult{Rel: job.File.Rel, Entry: entry, Err: err}:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}

	go func() {
		defer close(jobCh)
		for _, job := range jobs {
			select {
			case jobCh <- job:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(resultCh)
	}()
	return resultCh
}

func generateImageAssets(ctx context.Context, f ScanFile, output string, opts Options, deps Dependencies) (ManifestImage, error) {
	img, format, err := decodePreviewSource(ctx, f, output, opts.PreviewSize, deps)
	if err != nil {
		return ManifestImage{}, err
	}

	orientation := 1
	if f.IsImage && (f.Ext == ".jpg" || f.Ext == ".jpeg") {
		orientation = jpegOrientation(f.Abs)
	}
	img = orientImage(img, orientation)
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width < 1 || height < 1 {
		return ManifestImage{}, errors.New("empty preview image")
	}

	id := shortHash(f.Rel)
	thumbRel := filepath.ToSlash(filepath.Join("_gala", "thumbs", id+".jpg"))
	previewRel := filepath.ToSlash(filepath.Join("_gala", "previews", id+".jpg"))
	thumbAbs := filepath.Join(output, filepath.FromSlash(thumbRel))

	src := toNRGBA(img)
	thumb := makeSquareThumb(src, opts.ThumbSize)

	if err := writeJPEGAtomic(thumbAbs, thumb, 84); err != nil {
		return ManifestImage{}, err
	}

	previewAbs := filepath.Join(output, filepath.FromSlash(previewRel))
	preview := makePreview(src, opts.PreviewSize)
	if err := writeJPEGAtomic(previewAbs, preview, 87); err != nil {
		return ManifestImage{}, err
	}

	var exif *ExifInfo
	exifReady := false
	if deps.ExifTool != "" && (f.Kind == "image" || f.Kind == "raw") {
		exif, exifReady = extractExifInfo(ctx, f.Abs, deps)
	}

	return ManifestImage{
		Size: f.Size, ModNano: f.ModNano, Thumb: thumbRel, Preview: previewRel,
		Width: width, Height: height, SourceType: format,
		ExifReady: exifReady, Exif: exif,
	}, nil
}

func decodePreviewSource(ctx context.Context, f ScanFile, output string, previewSize int, deps Dependencies) (image.Image, string, error) {
	if f.IsImage {
		in, err := os.Open(f.Abs)
		if err != nil {
			return nil, "", err
		}
		defer in.Close()
		img, format, err := image.Decode(in)
		if err != nil {
			return nil, "", fmt.Errorf("decode: %w", err)
		}
		return img, format, nil
	}
	if f.Kind == "raw" {
		return decodeRAWPreview(ctx, f, previewSize, deps)
	}

	tmpDir, err := os.MkdirTemp(filepath.Join(output, "_gala"), ".render-*")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(tmpDir)

	var rendered string
	var sourceType string
	switch f.Kind {
	case "video":
		if deps.FFmpeg == "" {
			return nil, "", errors.New("ffmpeg is required for video previews")
		}
		rendered = filepath.Join(tmpDir, "frame.jpg")
		args := []string{
			"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
			"-i", f.Abs,
			"-vf", "thumbnail=100",
			"-frames:v", "1", "-q:v", "2", rendered,
		}
		if err := runExternal(ctx, 3*time.Minute, deps.FFmpeg, args...); err != nil {
			fallback := []string{
				"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
				"-ss", "1", "-i", f.Abs,
				"-frames:v", "1", "-q:v", "2", rendered,
			}
			if fallbackErr := runExternal(ctx, 3*time.Minute, deps.FFmpeg, fallback...); fallbackErr != nil {
				return nil, "", fmt.Errorf("ffmpeg thumbnail: %v; fallback: %w", err, fallbackErr)
			}
		}
		sourceType = "video/" + strings.TrimPrefix(f.Ext, ".")

	case "pdf":
		if deps.PDFToCairo == "" {
			return nil, "", errors.New("pdftocairo is required for PDF previews")
		}
		prefix := filepath.Join(tmpDir, "page")
		rendered = prefix + ".png"
		args := []string{
			"-f", "1", "-l", "1", "-singlefile", "-png",
			"-scale-to", strconv.Itoa(previewSize),
			f.Abs, prefix,
		}
		if err := runExternal(ctx, 2*time.Minute, deps.PDFToCairo, args...); err != nil {
			return nil, "", fmt.Errorf("pdftocairo: %w", err)
		}
		sourceType = "application/pdf"

	default:
		return nil, "", fmt.Errorf("unsupported preview type %q", f.Kind)
	}

	in, err := os.Open(rendered)
	if err != nil {
		return nil, "", fmt.Errorf("open rendered preview: %w", err)
	}
	defer in.Close()
	img, _, err := image.Decode(in)
	if err != nil {
		return nil, "", fmt.Errorf("decode rendered preview: %w", err)
	}
	return img, sourceType, nil
}

func decodeRAWPreview(ctx context.Context, f ScanFile, previewSize int, deps Dependencies) (image.Image, string, error) {
	var attempts []string
	decodeOutput := func(label string, output []byte, err error) (image.Image, bool) {
		if err != nil {
			attempts = append(attempts, label+": "+err.Error())
			return nil, false
		}
		if len(output) == 0 {
			attempts = append(attempts, label+": no embedded image")
			return nil, false
		}
		img, _, decodeErr := image.Decode(bytes.NewReader(output))
		if decodeErr != nil {
			attempts = append(attempts, label+": "+decodeErr.Error())
			return nil, false
		}
		img = orientImage(img, jpegOrientationReader(bytes.NewReader(output)))
		return img, true
	}

	if deps.ExifTool != "" {
		for _, tag := range []string{"PreviewImage", "JpgFromRaw", "OtherImage", "ThumbnailImage"} {
			output, err := runExternalOutput(ctx, 90*time.Second, deps.ExifTool, "-b", "-"+tag, f.Abs)
			if img, ok := decodeOutput("exiftool "+tag, output, err); ok {
				return img, "image/x-raw", nil
			}
		}
	}

	if deps.DCRaw != "" {
		output, err := runExternalOutput(ctx, 3*time.Minute, deps.DCRaw, "-e", "-c", f.Abs)
		if img, ok := decodeOutput("dcraw embedded preview", output, err); ok {
			return img, "image/x-raw", nil
		}
	}

	if deps.Magick != "" {
		geometry := fmt.Sprintf("%dx%d>", previewSize, previewSize)
		output, err := runExternalOutput(ctx, 5*time.Minute, deps.Magick,
			"-quiet", f.Abs, "-auto-orient", "-thumbnail", geometry, "jpg:-")
		if img, ok := decodeOutput("ImageMagick RAW render", output, err); ok {
			return img, "image/x-raw", nil
		}
	}

	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if len(attempts) == 0 {
		return nil, "", errors.New("no RAW preview helper is installed")
	}
	return nil, "", fmt.Errorf("RAW preview failed: %s", strings.Join(attempts, "; "))
}

func extractExifInfo(ctx context.Context, path string, deps Dependencies) (*ExifInfo, bool) {
	if deps.ExifTool == "" {
		return nil, false
	}
	output, err := runExternalOutput(ctx, 20*time.Second, deps.ExifTool,
		"-json",
		"-Make", "-Model", "-LensModel", "-DateTimeOriginal",
		"-ExposureTime", "-FNumber", "-ISO", "-FocalLength", "-ExposureCompensation",
		path,
	)
	if err != nil {
		return nil, true
	}
	var rows []map[string]any
	if err := json.Unmarshal(output, &rows); err != nil || len(rows) == 0 {
		return nil, true
	}
	info := normalizeExifInfo(rows[0])
	return info, true
}

func normalizeExifInfo(row map[string]any) *ExifInfo {
	makeValue := exifString(row["Make"])
	modelValue := exifString(row["Model"])
	camera := strings.TrimSpace(modelValue)
	if camera == "" {
		camera = strings.TrimSpace(makeValue)
	} else if makeValue != "" && !strings.HasPrefix(strings.ToLower(camera), strings.ToLower(strings.TrimSpace(makeValue))) {
		camera = strings.TrimSpace(makeValue + " " + camera)
	}
	lens := exifString(row["LensModel"])
	captureTime := formatExifDate(exifString(row["DateTimeOriginal"]))
	exposureTime := exifString(row["ExposureTime"])
	if exposureTime != "" && !strings.Contains(strings.ToLower(exposureTime), "s") {
		exposureTime += " s"
	}
	aperture := exifString(row["FNumber"])
	if aperture != "" && !strings.HasPrefix(strings.ToLower(aperture), "f/") {
		aperture = "f/" + aperture
	}
	iso := exifString(row["ISO"])
	focalLength := exifString(row["FocalLength"])
	if focalLength != "" && !strings.Contains(strings.ToLower(focalLength), "mm") {
		focalLength += " mm"
	}
	exposureComp := exifString(row["ExposureCompensation"])
	if exposureComp != "" && !strings.Contains(strings.ToUpper(exposureComp), "EV") {
		exposureComp += " EV"
	}
	info := &ExifInfo{
		Camera:               camera,
		Lens:                 lens,
		CaptureTime:          captureTime,
		ExposureTime:         exposureTime,
		Aperture:             aperture,
		ISO:                  iso,
		FocalLength:          focalLength,
		ExposureCompensation: exposureComp,
	}
	if info.Camera == "" && info.Lens == "" && info.CaptureTime == "" && info.ExposureTime == "" && info.Aperture == "" && info.ISO == "" && info.FocalLength == "" && info.ExposureCompensation == "" {
		return nil
	}
	return info
}

func exifString(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return x.String()
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func formatExifDate(s string) string {
	if len(s) >= 19 && s[4] == ':' && s[7] == ':' {
		return s[:4] + "-" + s[5:7] + "-" + s[8:]
	}
	return s
}

func encodeExifData(exif *ExifInfo) string {
	if exif == nil {
		return ""
	}
	b, err := json.Marshal(exif)
	if err != nil {
		return ""
	}
	return string(b)
}

func runExternal(ctx context.Context, timeout time.Duration, name string, args ...string) error {
	_, err := runExternalOutput(ctx, timeout, name, args...)
	return err
}

func runExternalOutput(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, name, args...)
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if commandCtx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("timed out after %s", timeout)
	}
	if err != nil {
		message := ""
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			message = strings.TrimSpace(string(exitErr.Stderr))
		}
		if message == "" {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s", err, message)
	}
	return output, nil
}

func shortHash(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:10])
}

func toNRGBA(src image.Image) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Over)
	return dst
}

func makeSquareThumb(src *image.NRGBA, size int) *image.NRGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	side := w
	if h < side {
		side = h
	}
	x := (w - side) / 2
	y := (h - side) / 2
	cropped := cropNRGBA(src, image.Rect(x, y, x+side, y+side))
	return resizeBilinear(cropped, size, size)
}

func makePreview(src *image.NRGBA, max int) *image.NRGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	if w <= max && h <= max {
		return src
	}
	scale := math.Min(float64(max)/float64(w), float64(max)/float64(h))
	nw := int(math.Round(float64(w) * scale))
	nh := int(math.Round(float64(h) * scale))
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	return resizeBilinear(src, nw, nh)
}

func cropNRGBA(src *image.NRGBA, r image.Rectangle) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(dst, dst.Bounds(), src, r.Min, draw.Src)
	return dst
}

func resizeBilinear(src *image.NRGBA, dw, dh int) *image.NRGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	if sw == dw && sh == dh {
		return src
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	if dw == 1 || dh == 1 || sw == 1 || sh == 1 {
		for y := 0; y < dh; y++ {
			sy := y * sh / dh
			for x := 0; x < dw; x++ {
				sx := x * sw / dw
				si := sy*src.Stride + sx*4
				di := y*dst.Stride + x*4
				copy(dst.Pix[di:di+4], src.Pix[si:si+4])
			}
		}
		return dst
	}

	xScale := float64(sw-1) / float64(dw-1)
	yScale := float64(sh-1) / float64(dh-1)
	for y := 0; y < dh; y++ {
		fy := float64(y) * yScale
		y0 := int(fy)
		y1 := y0 + 1
		if y1 >= sh {
			y1 = sh - 1
		}
		wy := fy - float64(y0)
		for x := 0; x < dw; x++ {
			fx := float64(x) * xScale
			x0 := int(fx)
			x1 := x0 + 1
			if x1 >= sw {
				x1 = sw - 1
			}
			wx := fx - float64(x0)

			i00 := y0*src.Stride + x0*4
			i10 := y0*src.Stride + x1*4
			i01 := y1*src.Stride + x0*4
			i11 := y1*src.Stride + x1*4
			di := y*dst.Stride + x*4
			for c := 0; c < 4; c++ {
				top := float64(src.Pix[i00+c])*(1-wx) + float64(src.Pix[i10+c])*wx
				bottom := float64(src.Pix[i01+c])*(1-wx) + float64(src.Pix[i11+c])*wx
				dst.Pix[di+c] = uint8(math.Round(top*(1-wy) + bottom*wy))
			}
		}
	}
	return dst
}

func writeJPEGAtomic(path string, img image.Image, quality int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gala-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := jpeg.Encode(tmp, img, &jpeg.Options{Quality: quality}); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(tmpName, path)
}

func replaceFile(tmp, target string) error {
	if err := os.Rename(tmp, target); err == nil {
		return nil
	}
	_ = os.Remove(target)
	return os.Rename(tmp, target)
}

func chooseFolderCovers(dirs map[string]*dirData, files []ScanFile, images map[string]ManifestImage) map[string]string {
	covers := make(map[string]string)
	for _, f := range files {
		if f.Kind != "image" && f.Kind != "raw" {
			continue
		}
		if _, ok := images[f.Rel]; !ok {
			continue
		}
		d := f.DirRel
		for {
			if _, exists := covers[d]; !exists {
				covers[d] = f.Rel
			}
			if d == "" {
				break
			}
			d = pathDir(d)
		}
	}
	return covers
}

func pathDir(rel string) string {
	d := filepath.ToSlash(filepath.Dir(filepath.FromSlash(rel)))
	if d == "." {
		return ""
	}
	return d
}

func writePages(source, output string, opts Options, dirs map[string]*dirData, files []ScanFile, images map[string]ManifestImage, covers map[string]string) ([]string, error) {
	tmpl, err := template.New("page").Parse(pageTemplate)
	if err != nil {
		return nil, err
	}

	dirKeys := make([]string, 0, len(dirs))
	for rel := range dirs {
		dirKeys = append(dirKeys, rel)
	}
	sort.Slice(dirKeys, func(i, j int) bool {
		return strings.ToLower(dirKeys[i]) < strings.ToLower(dirKeys[j])
	})

	videos := make([]ScanFile, 0)
	for _, f := range files {
		if f.Kind == "video" {
			videos = append(videos, f)
		}
	}

	pages := make([]string, 0, len(dirKeys)+1)
	sourceName := filepath.Base(source)
	generatedAt := time.Now().Format("2006-01-02 15:04")
	rootIndex := filepath.Join(output, "index.html")
	videoCollectionDir := filepath.Join(output, "_gala", "collections", "videos")
	videoCollectionIndex := filepath.Join(videoCollectionDir, "index.html")

	for _, rel := range dirKeys {
		d := dirs[rel]
		pageDir := filepath.Join(output, filepath.FromSlash(rel))
		pagePath := filepath.Join(pageDir, "index.html")
		if err := os.MkdirAll(pageDir, 0o755); err != nil {
			return nil, err
		}

		data := PageData{
			Title: sourceName, SourceName: sourceName,
			CSSURL:      withVersion(webRel(pageDir, filepath.Join(output, "_gala", "gala.css")), version),
			JSURL:       withVersion(webRel(pageDir, filepath.Join(output, "_gala", "gala.js")), version),
			GeneratedAt: generatedAt,
		}
		if rel != "" {
			data.Title = filepath.Base(filepath.FromSlash(rel))
		}
		data.Breadcrumbs = makeBreadcrumbs(output, pageDir, rel, sourceName)

		for _, childRel := range d.Folders {
			childDir := filepath.Join(output, filepath.FromSlash(childRel))
			fc := FolderCard{
				Name:  filepath.Base(filepath.FromSlash(childRel)),
				Href:  webRel(pageDir, filepath.Join(childDir, "index.html")),
				Count: len(dirs[childRel].Folders) + len(dirs[childRel].Files),
			}
			if coverRel, ok := covers[childRel]; ok {
				if entry, ok := images[coverRel]; ok {
					fc.HasThumb = true
					fc.ThumbURL = withVersion(webRel(pageDir, filepath.Join(output, filepath.FromSlash(entry.Thumb))), cacheToken(entry, opts))
				}
			}
			data.Folders = append(data.Folders, fc)
		}

		if rel == "" && len(videos) > 0 {
			collection := FolderCard{
				Name:    "Videos",
				Href:    webRel(pageDir, videoCollectionIndex),
				Count:   len(videos),
				Virtual: true,
			}
			for _, videoFile := range videos {
				if entry, ok := images[videoFile.Rel]; ok {
					collection.HasThumb = true
					collection.ThumbURL = withVersion(webRel(pageDir, filepath.Join(output, filepath.FromSlash(entry.Thumb))), cacheToken(entry, opts))
					break
				}
			}
			data.Folders = append([]FolderCard{collection}, data.Folders...)
		}

		for _, f := range d.Files {
			if entry, ok := images[f.Rel]; ok && f.IsPreviewable {
				data.Images = append(data.Images, makeImageCard(source, output, pageDir, opts, f, entry, f.Name))
				continue
			}
			data.Files = append(data.Files, makeFileCard(source, output, pageDir, opts, f, f.Name))
		}
		data.Empty = len(data.Folders) == 0 && len(data.Images) == 0 && len(data.Files) == 0

		if err := writeTemplateAtomic(pagePath, tmpl, data); err != nil {
			return nil, err
		}
		pageRel, _ := filepath.Rel(output, pagePath)
		pages = append(pages, filepath.ToSlash(pageRel))
	}

	if len(videos) > 0 {
		if err := os.MkdirAll(videoCollectionDir, 0o755); err != nil {
			return nil, err
		}
		data := PageData{
			Title:       "Videos",
			SourceName:  sourceName,
			CSSURL:      withVersion(webRel(videoCollectionDir, filepath.Join(output, "_gala", "gala.css")), version),
			JSURL:       withVersion(webRel(videoCollectionDir, filepath.Join(output, "_gala", "gala.js")), version),
			GeneratedAt: generatedAt,
			Breadcrumbs: []Link{
				{Label: sourceName, Href: webRel(videoCollectionDir, rootIndex)},
				{Label: "Videos"},
			},
		}
		for _, f := range videos {
			if entry, ok := images[f.Rel]; ok {
				data.Images = append(data.Images, makeImageCard(source, output, videoCollectionDir, opts, f, entry, f.Rel))
			} else {
				data.Files = append(data.Files, makeFileCard(source, output, videoCollectionDir, opts, f, f.Rel))
			}
		}
		data.Empty = len(data.Images) == 0 && len(data.Files) == 0
		if err := writeTemplateAtomic(videoCollectionIndex, tmpl, data); err != nil {
			return nil, err
		}
		pageRel, _ := filepath.Rel(output, videoCollectionIndex)
		pages = append(pages, filepath.ToSlash(pageRel))
	}

	return pages, nil
}

func makeImageCard(source, output, pageDir string, opts Options, f ScanFile, entry ManifestImage, displayName string) ImageCard {
	original := originalLink(opts, source, output, pageDir, f.Rel)
	token := cacheToken(entry, opts)
	thumbURL := withVersion(webRel(pageDir, filepath.Join(output, filepath.FromSlash(entry.Thumb))), token)
	mediumURL := withFileName(
		withVersion(webRel(pageDir, filepath.Join(output, filepath.FromSlash(entry.Preview))), token),
		f.Name,
	)

	previewURL := mediumURL
	posterURL := ""
	badge := ""
	switch f.Kind {
	case "video":
		badge = "▶"
		previewURL = original
		posterURL = mediumURL
	case "pdf":
		badge = "PDF"
	case "raw":
		badge = "RAW"
	}

	return ImageCard{
		Name: displayName, FileName: filepath.Base(f.Name), MediaID: shortHash(f.Rel), OriginalURL: original,
		ThumbURL: thumbURL, PreviewURL: previewURL, PosterURL: posterURL,
		ExifJSON: encodeExifData(entry.Exif),
		Width:    entry.Width, Height: entry.Height,
		Kind: f.Kind, Badge: badge,
	}
}

func makeFileCard(source, output, pageDir string, opts Options, f ScanFile, displayName string) FileCard {
	ext := strings.TrimPrefix(strings.ToUpper(f.Ext), ".")
	if ext == "" {
		ext = "FILE"
	}
	return FileCard{
		Name:        displayName,
		OriginalURL: originalLink(opts, source, output, pageDir, f.Rel),
		Extension:   ext,
		Size:        humanSize(f.Size),
	}
}

func makeBreadcrumbs(output, pageDir, rel, sourceName string) []Link {
	rootIndex := filepath.Join(output, "index.html")
	crumbs := []Link{{Label: sourceName, Href: webRel(pageDir, rootIndex)}}
	if rel == "" {
		crumbs[0].Href = ""
		return crumbs
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for i, part := range parts {
		joined := filepath.Join(parts[:i+1]...)
		target := filepath.Join(output, joined, "index.html")
		href := webRel(pageDir, target)
		if i == len(parts)-1 {
			href = ""
		}
		crumbs = append(crumbs, Link{Label: part, Href: href})
	}
	return crumbs
}

func originalLink(opts Options, source, output, pageDir, rel string) string {
	if opts.OriginalURL != "" {
		return strings.TrimRight(opts.OriginalURL, "/") + "/" + escapeRelURL(rel)
	}
	return webRel(pageDir, filepath.Join(source, filepath.FromSlash(rel)))
}

func webRel(fromDir, target string) string {
	rel, err := filepath.Rel(fromDir, target)
	if err != nil {
		return filepath.ToSlash(target)
	}
	return escapeRelURL(filepath.ToSlash(rel))
}

func withVersion(rawURL, token string) string {
	if token == "" {
		return rawURL
	}
	separator := "?"
	if strings.Contains(rawURL, "?") {
		separator = "&"
	}
	return rawURL + separator + "v=" + url.QueryEscape(token)
}

func withFileName(rawURL, name string) string {
	if name == "" {
		return rawURL
	}
	separator := "?"
	if strings.Contains(rawURL, "?") {
		separator = "&"
	}
	return rawURL + separator + "filename=" + url.QueryEscape(filepath.Base(name))
}

func cacheToken(entry ManifestImage, opts Options) string {
	return version + "-" + strconv.FormatInt(entry.ModNano, 36) + "-" + strconv.FormatInt(entry.Size, 36) +
		"-t" + strconv.Itoa(opts.ThumbSize) + "-p" + strconv.Itoa(opts.PreviewSize)
}

func escapeRelURL(rel string) string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for i, p := range parts {
		if p == "." || p == ".." || p == "" {
			continue
		}
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for value := n / unit; value >= unit && exp < 4; value /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func writeTemplateAtomic(path string, tmpl *template.Template, data PageData) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gala-*.html")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmpl.Execute(tmp, data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(name, path)
}

func writeJSONAtomic(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return writeBytesAtomic(path, b, 0o644)
}

func writeStaticAssets(output string) error {
	assetDir := filepath.Join(output, "_gala")
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		return err
	}
	if err := writeBytesAtomic(filepath.Join(assetDir, "gala.css"), []byte(galaCSS), 0o644); err != nil {
		return err
	}
	return writeBytesAtomic(filepath.Join(assetDir, "gala.js"), []byte(galaJS), 0o644)
}

func writeBytesAtomic(path string, b []byte, mode fs.FileMode) error {
	if old, err := os.ReadFile(path); err == nil && string(old) == string(b) {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gala-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(name, path)
}

func cleanupStale(output string, old Manifest, current map[string]ManifestImage, pages []string) {
	for rel, entry := range old.Images {
		newEntry, ok := current[rel]
		if entry.Thumb != "" && (!ok || newEntry.Thumb != entry.Thumb) {
			_ = os.Remove(filepath.Join(output, filepath.FromSlash(entry.Thumb)))
		}
		if entry.Preview != "" && (!ok || newEntry.Preview != entry.Preview) {
			_ = os.Remove(filepath.Join(output, filepath.FromSlash(entry.Preview)))
		}
	}
	pageSet := make(map[string]bool, len(pages))
	for _, p := range pages {
		pageSet[p] = true
	}
	for _, p := range old.Pages {
		if !pageSet[p] {
			_ = os.Remove(filepath.Join(output, filepath.FromSlash(p)))
		}
	}
	removeEmptyDirs(output)
}

func removeEmptyDirs(root string) {
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && path != root && filepath.Base(path) != "_gala" {
			dirs = append(dirs, path)
		}
		return nil
	})
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, d := range dirs {
		_ = os.Remove(d)
	}
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir() && st.Size() > 0
}

func manifestAssetsExist(output, kind string, entry ManifestImage) bool {
	if entry.Thumb == "" || !fileExists(filepath.Join(output, filepath.FromSlash(entry.Thumb))) {
		return false
	}
	return entry.Preview != "" && fileExists(filepath.Join(output, filepath.FromSlash(entry.Preview)))
}

// jpegOrientation reads the EXIF Orientation tag without pulling in a dependency.
// Invalid or unsupported metadata simply returns the normal orientation (1).
func jpegOrientation(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 1
	}
	defer f.Close()
	return jpegOrientationReader(f)
}

func jpegOrientationReader(r io.Reader) int {
	var soi [2]byte
	if _, err := io.ReadFull(r, soi[:]); err != nil || soi != [2]byte{0xff, 0xd8} {
		return 1
	}
	for {
		var marker [2]byte
		if _, err := io.ReadFull(r, marker[:]); err != nil {
			return 1
		}
		for marker[0] != 0xff {
			marker[0] = marker[1]
			if _, err := io.ReadFull(r, marker[1:]); err != nil {
				return 1
			}
		}
		if marker[1] == 0xd9 || marker[1] == 0xda {
			return 1
		}
		if marker[1] >= 0xd0 && marker[1] <= 0xd7 {
			continue
		}
		var lenBuf [2]byte
		if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
			return 1
		}
		segLen := int(binary.BigEndian.Uint16(lenBuf[:])) - 2
		if segLen < 0 || segLen > 16*1024*1024 {
			return 1
		}
		data := make([]byte, segLen)
		if _, err := io.ReadFull(r, data); err != nil {
			return 1
		}
		if marker[1] == 0xe1 && len(data) > 14 && string(data[:6]) == "Exif\x00\x00" {
			return parseTIFFOrientation(data[6:])
		}
	}
}

func parseTIFFOrientation(data []byte) int {
	if len(data) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(data[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(data[2:4]) != 42 {
		return 1
	}
	off := int(order.Uint32(data[4:8]))
	if off < 0 || off+2 > len(data) {
		return 1
	}
	count := int(order.Uint16(data[off : off+2]))
	pos := off + 2
	for i := 0; i < count; i++ {
		if pos+12 > len(data) {
			return 1
		}
		tag := order.Uint16(data[pos : pos+2])
		if tag == 0x0112 {
			typ := order.Uint16(data[pos+2 : pos+4])
			n := order.Uint32(data[pos+4 : pos+8])
			if typ == 3 && n >= 1 {
				v := int(order.Uint16(data[pos+8 : pos+10]))
				if v >= 1 && v <= 8 {
					return v
				}
			}
			return 1
		}
		pos += 12
	}
	return 1
}

func orientImage(src image.Image, orientation int) image.Image {
	if orientation < 2 || orientation > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	rotated := orientation >= 5 && orientation <= 8
	dw, dh := w, h
	if rotated {
		dw, dh = h, w
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch orientation {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

const pageTemplate = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="color-scheme" content="dark light">
  <title>{{.Title}} · Gala</title>
  <link rel="stylesheet" href="{{.CSSURL}}">
  <script defer src="{{.JSURL}}"></script>
</head>
<body>
<header class="site-header">
  <nav class="breadcrumbs" aria-label="Breadcrumbs">
    {{range $i, $b := .Breadcrumbs}}{{if $i}}<span class="separator">/</span>{{end}}{{if $b.Href}}<a href="{{$b.Href}}">{{$b.Label}}</a>{{else}}<span aria-current="page">{{$b.Label}}</span>{{end}}{{end}}
  </nav>
</header>

<main>

  {{if .Folders}}
  <section aria-labelledby="folders-heading">
    <h2 id="folders-heading">Folders</h2>
    <div class="grid">
      {{range .Folders}}
      <a class="card folder-card{{if .Virtual}} virtual-folder-card{{end}}" href="{{.Href}}">
        <div class="thumb folder-thumb">
          {{if .HasThumb}}<img src="{{.ThumbURL}}" alt="" loading="lazy">{{else}}<div class="folder-placeholder">G</div>{{end}}
          {{if .Virtual}}
          <span class="folder-badge virtual-folder-badge" aria-hidden="true"><svg viewBox="0 0 24 24"><path class="collection-back" d="M5 3h12a2 2 0 0 1 2 2v1H7a3 3 0 0 0-3 3v8H3a2 2 0 0 1-2-2V7a4 4 0 0 1 4-4z"/><path class="collection-front" d="M7 7h12a3 3 0 0 1 3 3v8a3 3 0 0 1-3 3H7a3 3 0 0 1-3-3v-8a3 3 0 0 1 3-3z"/><path class="collection-play" d="m11 11 5 3-5 3z"/></svg></span>
          {{else}}
          <span class="folder-badge" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M3 5a2 2 0 0 1 2-2h5l2 2h7a2 2 0 0 1 2 2v10a3 3 0 0 1-3 3H5a2 2 0 0 1-2-2V5z"/></svg></span>
          {{end}}
        </div>
        <div class="card-text"><strong>{{.Name}}</strong><span>{{.Count}} item{{if ne .Count 1}}s{{end}}</span></div>
      </a>
      {{end}}
    </div>
  </section>
  {{end}}

  {{if .Images}}
  <section aria-labelledby="media-heading">
    <h2 id="media-heading">Media</h2>
    <div class="grid image-grid">
      {{range .Images}}
      <a class="card media-card {{.Kind}}" href="{{.PreviewURL}}" data-kind="{{.Kind}}" data-media-id="{{.MediaID}}" data-preview="{{.PreviewURL}}" data-poster="{{.PosterURL}}" data-original="{{.OriginalURL}}" data-name="{{.Name}}" data-filename="{{.FileName}}" data-dimensions="{{.Width}} × {{.Height}}" data-exif='{{.ExifJSON}}'>
        <div class="thumb"><img src="{{.ThumbURL}}" alt="{{.Name}}" loading="lazy">{{if .Badge}}<span class="media-badge" aria-hidden="true">{{.Badge}}</span>{{end}}</div>
        <div class="card-text"><strong>{{.Name}}</strong><span>{{.Width}} × {{.Height}}</span></div>
      </a>
      {{end}}
    </div>
  </section>
  {{end}}

  {{if .Files}}
  <section aria-labelledby="files-heading">
    <h2 id="files-heading">Files</h2>
    <div class="file-list">
      {{range .Files}}
      <a class="file-row" href="{{.OriginalURL}}">
        <span class="file-icon">{{.Extension}}</span>
        <span class="file-name">{{.Name}}</span>
        <span class="file-size">{{.Size}}</span>
      </a>
      {{end}}
    </div>
  </section>
  {{end}}

  {{if .Empty}}<p class="empty">This folder is empty.</p>{{end}}
</main>

<footer><span>Generated by Gala · {{.GeneratedAt}}</span></footer>

<div class="lightbox" id="lightbox" hidden aria-hidden="true">
  <button class="close" type="button" aria-label="Close">×</button>
  <button class="nav prev" type="button" aria-label="Previous" {{if lt (len .Images) 2}}hidden{{end}}>‹</button>
  <div class="lightbox-stage" title="Click the top area to close">
    <span class="lightbox-hint exit-hint" aria-hidden="true">Click to exit</span>
    <img class="lightbox-image" alt="">
    <video class="lightbox-video" controls preload="metadata" hidden></video>
    <span class="lightbox-hint download-hint" aria-hidden="true">Click for download</span>
  </div>
  <button class="nav next" type="button" aria-label="Next" {{if lt (len .Images) 2}}hidden{{end}}>›</button>
  <div class="lightbox-info" hidden>
    <div class="lightbox-details">
      <div><strong class="lightbox-name"></strong><span class="lightbox-dimensions"></span></div>
      <div class="lightbox-exif" hidden></div>
    </div>
    <div class="lightbox-actions">
      <button class="copy-link" type="button">Copy link</button>
      <a class="download-link" href="" download>Download original</a>
    </div>
  </div>
</div>
</body>
</html>
`

const galaCSS = `:root {
  --bg: #111318;
  --panel: #1b1f27;
  --panel-2: #252b36;
  --text: #f1f3f7;
  --muted: #9ca6b5;
  --accent: #e9b44c;
  --folder: #4f9ddf;
  --virtual-folder: #a86de0;
  --gap: clamp(.75rem, 2vw, 1.25rem);
  --info-height: 0px;
}
* { box-sizing: border-box; }
html { background: var(--bg); color: var(--text); font-family: system-ui, sans-serif; }
body { margin: 0; min-height: 100vh; }
a { color: inherit; text-decoration: none; }
.site-header { position: sticky; top: 0; z-index: 10; padding: 1rem clamp(1rem, 4vw, 3rem); background: color-mix(in srgb, var(--bg) 88%, transparent); backdrop-filter: blur(12px); }
.breadcrumbs { min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; text-align: center; color: var(--muted); font-size: clamp(1.05rem, 2.2vw, 1.45rem); font-weight: 650; line-height: 1.25; }
.breadcrumbs a:hover { color: var(--text); }
.breadcrumbs [aria-current="page"] { color: var(--text); }
.separator { padding: 0 .4rem; opacity: .4; font-weight: 400; }
main { width: min(1500px, 100%); margin: auto; padding: clamp(1rem, 4vw, 3rem); }
h2 { margin: 2rem 0 .9rem; color: var(--muted); font-size: .8rem; letter-spacing: .13em; text-transform: uppercase; }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(170px, 1fr)); gap: var(--gap); }
.card { min-width: 0; overflow: hidden; border: 1px solid #ffffff12; border-radius: 14px; background: var(--panel); transition: transform .15s ease, border-color .15s ease; }
.card:hover { transform: translateY(-3px); border-color: #ffffff40; }
.thumb { position: relative; aspect-ratio: 1; overflow: hidden; background: var(--panel-2); }
.thumb img { width: 100%; height: 100%; object-fit: cover; display: block; }
.folder-placeholder { display: grid; place-items: center; width: 100%; height: 100%; font-size: 5rem; font-weight: 900; color: #ffffff14; }
.folder-badge, .media-badge { position: absolute; right: .65rem; bottom: .65rem; display: grid; place-items: center; min-width: 2.3rem; height: 2.3rem; padding: 0 .55rem; border-radius: 999px; box-shadow: 0 6px 18px #0008; color: white; font: 800 .7rem system-ui, sans-serif; }
.folder-badge { width: 2.3rem; padding: 0; background: var(--folder); }
.folder-badge svg { width: 1.35rem; fill: white; }
.virtual-folder-badge { background: var(--virtual-folder); }
.virtual-folder-badge svg { width: 1.5rem; }
.virtual-folder-badge .collection-back { opacity: .55; }
.virtual-folder-badge .collection-front { fill: white; }
.virtual-folder-badge .collection-play { fill: var(--virtual-folder); }
.media-badge { background: #10131ad9; border: 1px solid #ffffff35; backdrop-filter: blur(5px); }
.video .media-badge { min-width: auto; height: auto; padding: 0; border: 0; border-radius: 0; background: transparent; box-shadow: none; backdrop-filter: none; font-size: 2rem; line-height: 1; text-shadow: 0 4px 12px #000; }
.pdf .media-badge { color: #ffb2aa; }
.card-text { padding: .75rem .85rem .9rem; display: grid; gap: .2rem; }
.card-text strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: .92rem; }
.card-text span { color: var(--muted); font-size: .78rem; }
.file-list { display: grid; gap: .45rem; }
.file-row { display: grid; grid-template-columns: 4rem minmax(0,1fr) auto; align-items: center; gap: .8rem; padding: .7rem; border-radius: 10px; background: var(--panel); border: 1px solid #ffffff0d; }
.file-row:hover { border-color: #ffffff35; }
.file-icon { padding: .35rem .45rem; text-align: center; border-radius: 6px; background: var(--panel-2); color: var(--accent); font: 700 .68rem ui-monospace, monospace; }
.file-name { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.file-size { color: var(--muted); font-size: .8rem; }
.empty { color: var(--muted); }
footer { min-height: 4.75rem; padding: 2rem; text-align: center; color: var(--muted); font-size: .75rem; }
footer span { opacity: 0; transition: opacity .18s ease; }
footer:hover span { opacity: 1; }
.lightbox { position: fixed; inset: 0; z-index: 100; background: #050609f5; }
.lightbox[hidden] { display: none; }
.lightbox-stage { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; overflow: hidden; padding: 4rem 4.5rem 1rem; cursor: pointer; transition: bottom .15s ease; }
.lightbox.info-open .lightbox-stage { bottom: var(--info-height); }
.lightbox-image, .lightbox-video { display: block; width: auto; height: auto; max-width: 100%; max-height: 100%; object-fit: contain; filter: drop-shadow(0 20px 55px #000); user-select: none; -webkit-user-drag: none; }
.lightbox-image[hidden], .lightbox-video[hidden] { display: none; }
.lightbox-video { background: #000; cursor: default; }
.lightbox-hint { position: absolute; z-index: 5; left: 50%; translate: -50% 0; padding: .5rem .8rem; border-radius: 999px; background: #050609c9; border: 1px solid #ffffff25; color: #fff; font-size: .78rem; font-weight: 700; letter-spacing: .02em; opacity: 0; transform: translateY(-4px); transition: opacity .12s ease, transform .12s ease; pointer-events: none; }
.lightbox-hint.visible { opacity: 1; transform: translateY(0); }
.exit-hint { top: .8rem; }
.download-hint { bottom: 1rem; }
.close, .nav { position: absolute; z-index: 3; border: 0; color: white; background: #0007; cursor: pointer; }
.close { right: .75rem; top: .75rem; width: 3.2rem; height: 3.2rem; border-radius: 50%; font-size: 2.2rem; line-height: 1; }
.nav { top: 45%; width: 3rem; height: 4rem; font-size: 3rem; border-radius: 9px; }
.nav[hidden] { display: none; }
.prev { left: .7rem; }
.next { right: .7rem; }
.lightbox-info { position: absolute; z-index: 4; left: 0; right: 0; bottom: 0; display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; padding: 1rem clamp(1rem, 4vw, 3rem); background: #10131aeb; border-top: 1px solid #ffffff20; }
.lightbox-info[hidden] { display: none; }
.lightbox-details { display: grid; gap: .45rem; min-width: 0; }
.lightbox-details > div:first-child { display: grid; gap: .2rem; }
.lightbox-exif { display: grid; gap: .22rem; color: var(--muted); font-size: .8rem; }
.lightbox-exif[hidden] { display: none; }
.lightbox-exif strong { color: var(--text); }
.lightbox-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.lightbox-dimensions { color: var(--muted); font-size: .8rem; }
.lightbox-actions { display: flex; align-items: center; gap: .75rem; flex: none; }
.copy-link, .download-link { flex: none; padding: .7rem 1rem; border-radius: 8px; font-weight: 750; }
.copy-link { border: 1px solid #ffffff25; background: #ffffff10; color: var(--text); cursor: pointer; }
.download-link { background: var(--accent); color: #17120a; }
@media (max-width: 640px) {
  .grid { grid-template-columns: repeat(2, minmax(0,1fr)); }
  .lightbox-stage { padding: 3.75rem .5rem .5rem; }
  .nav { display: none; }
  .lightbox-info { align-items: stretch; flex-direction: column; }
  .download-link { text-align: center; }
}
@media (hover: none) {
  .lightbox-hint { display: none; }
}
`

const galaJS = `(() => {
  const cards = [...document.querySelectorAll('.media-card')];
  const box = document.getElementById('lightbox');
  if (!box || cards.length === 0) return;

  const image = box.querySelector('.lightbox-image');
  const video = box.querySelector('.lightbox-video');
  const info = box.querySelector('.lightbox-info');
  const name = box.querySelector('.lightbox-name');
  const dimensions = box.querySelector('.lightbox-dimensions');
  const exif = box.querySelector('.lightbox-exif');
  const download = box.querySelector('.download-link');
  const copyLink = box.querySelector('.copy-link');
  const stage = box.querySelector('.lightbox-stage');
  const closeButton = box.querySelector('.close');
  const previousButton = box.querySelector('.prev');
  const nextButton = box.querySelector('.next');
  const exitHint = box.querySelector('.exit-hint');
  const downloadHint = box.querySelector('.download-hint');
  const byMediaId = new Map(cards.map((card, i) => [card.dataset.mediaId || String(i), i]));
  const hasMultiple = cards.length > 1;
  let index = 0;
  let currentKind = '';
  let touchX = null;

  function stopVideo() {
    video.pause();
    video.removeAttribute('src');
    video.removeAttribute('poster');
    video.load();
    video.hidden = true;
  }

  function activeMedia() {
    return currentKind === 'video' ? video : image;
  }

  function currentMediaID() {
    return cards[index].dataset.mediaId || String(index);
  }

  function currentShareURL() {
    const url = new URL(window.location.href);
    url.hash = 'media=' + encodeURIComponent(currentMediaID()) + '&filename=' + encodeURIComponent(cards[index].dataset.filename || '');
    return url.toString();
  }

  function syncURL() {
    history.replaceState(null, '', currentShareURL());
  }

  function clearURL() {
    history.replaceState(null, '', location.pathname + location.search);
  }

  function updateNavigation() {
    previousButton.hidden = !hasMultiple || index === 0;
    nextButton.hidden = !hasMultiple || index === cards.length - 1;
  }

  function hideHints() {
    exitHint.classList.remove('visible');
    downloadHint.classList.remove('visible');
  }

  function escapeHTML(value) {
    return String(value)
      .replaceAll('&', '&amp;')
      .replaceAll('<', '&lt;')
      .replaceAll('>', '&gt;')
      .replaceAll('"', '&quot;');
  }

  function renderExif(card) {
    const raw = card.dataset.exif || '';
    if (!raw) {
      exif.hidden = true;
      exif.innerHTML = '';
      return;
    }
    let data;
    try {
      data = JSON.parse(raw);
    } catch {
      exif.hidden = true;
      exif.innerHTML = '';
      return;
    }
    const lines = [];
    if (data.camera) lines.push('<div><strong>' + escapeHTML(data.camera) + '</strong></div>');
    if (data.lens) lines.push('<div>' + escapeHTML(data.lens) + '</div>');
    const exposure = [];
    if (data.exposureTime) exposure.push(escapeHTML(data.exposureTime));
    if (data.aperture) exposure.push(escapeHTML(data.aperture));
    if (data.iso) exposure.push('ISO ' + escapeHTML(data.iso));
    if (data.focalLength) exposure.push(escapeHTML(data.focalLength));
    if (data.exposureCompensation) exposure.push(escapeHTML(data.exposureCompensation));
    if (exposure.length) lines.push('<div>' + exposure.join(' · ') + '</div>');
    if (data.captureTime) lines.push('<div>' + escapeHTML(data.captureTime) + '</div>');
    if (!lines.length) {
      exif.hidden = true;
      exif.innerHTML = '';
      return;
    }
    exif.hidden = false;
    exif.innerHTML = lines.join('');
  }

  function show(i) {
    index = Math.max(0, Math.min(cards.length - 1, i));
    const card = cards[index];
    currentKind = card.dataset.kind || 'image';
    stopVideo();
    image.hidden = true;
    image.removeAttribute('src');

    if (currentKind === 'video') {
      video.poster = card.dataset.poster || '';
      video.src = card.dataset.original;
      video.hidden = false;
      video.load();
    } else {
      image.src = card.dataset.preview;
      image.alt = card.dataset.name || '';
      image.hidden = false;
    }
    name.textContent = card.dataset.name || '';
    dimensions.textContent = card.dataset.dimensions || '';
    download.href = card.dataset.original;
    renderExif(card);
    hideHints();
    updateNavigation();
    if (!box.hidden) syncURL();
  }

  function setInfoVisible(visible) {
    info.hidden = !visible;
    box.classList.toggle('info-open', visible);
    if (!visible) {
      box.style.setProperty('--info-height', '0px');
      return;
    }
    downloadHint.classList.remove('visible');
    requestAnimationFrame(() => {
      box.style.setProperty('--info-height', info.offsetHeight + 'px');
    });
  }

  function open(i) {
    box.hidden = false;
    box.setAttribute('aria-hidden', 'false');
    document.body.style.overflow = 'hidden';
    setInfoVisible(false);
    show(i);
  }

  function close() {
    box.hidden = true;
    box.setAttribute('aria-hidden', 'true');
    setInfoVisible(false);
    image.removeAttribute('src');
    image.hidden = false;
    stopVideo();
    currentKind = '';
    hideHints();
    document.body.style.overflow = '';
    clearURL();
  }

  function openFromLocation() {
    const hash = location.hash.startsWith('#') ? location.hash.slice(1) : location.hash;
    const params = new URLSearchParams(hash);
    const id = params.get('media');
    if (!id) {
      if (!box.hidden) close();
      return;
    }
    const found = byMediaId.get(id);
    if (found === undefined) return;
    if (box.hidden) open(found);
    else show(found);
  }

  cards.forEach((card, i) => card.addEventListener('click', event => {
    event.preventDefault();
    open(i);
  }));

  closeButton.addEventListener('click', close);
  previousButton.addEventListener('click', event => {
    event.stopPropagation();
    if (index > 0) show(index - 1);
  });
  nextButton.addEventListener('click', event => {
    event.stopPropagation();
    if (index < cards.length - 1) show(index + 1);
  });
  copyLink.addEventListener('click', async event => {
    event.stopPropagation();
    const oldLabel = copyLink.textContent;
    try {
      await navigator.clipboard.writeText(currentShareURL());
      copyLink.textContent = 'Copied';
    } catch {
      copyLink.textContent = 'Copy failed';
    }
    window.setTimeout(() => {
      copyLink.textContent = oldLabel;
    }, 1200);
  });

  stage.addEventListener('mousemove', event => {
    const media = activeMedia();
    const mediaRect = media.getBoundingClientRect();
    const closeBand = Math.max(44, Math.min(90, mediaRect.height * 0.12));
    const topOfMedia = event.clientY >= mediaRect.top && event.clientY <= mediaRect.top + closeBand;
    const lowerZone = event.clientY > window.innerHeight * 0.72;
    const overVideoControls = currentKind === 'video' && event.target === video;
    exitHint.classList.toggle('visible', topOfMedia || event.clientY < 56);
    downloadHint.classList.toggle('visible', lowerZone && info.hidden && !overVideoControls);
  });
  stage.addEventListener('mouseleave', hideHints);

  stage.addEventListener('click', event => {
    const media = activeMedia();
    const mediaRect = media.getBoundingClientRect();
    const closeBand = Math.max(44, Math.min(90, mediaRect.height * 0.12));
    const topOfMedia = event.clientY >= mediaRect.top && event.clientY <= mediaRect.top + closeBand;
    if (topOfMedia || event.clientY < 56) {
      close();
      return;
    }

    if (currentKind === 'video' && event.target === video) return;

    const lowerZone = event.clientY > window.innerHeight * 0.72;
    if (lowerZone) {
      setInfoVisible(info.hidden);
      return;
    }

    if (event.clientX < window.innerWidth / 2) {
      if (index > 0) show(index - 1);
    } else if (index < cards.length - 1) {
      show(index + 1);
    }
  });

  info.addEventListener('click', event => event.stopPropagation());
  download.addEventListener('click', event => event.stopPropagation());

  stage.addEventListener('touchstart', event => {
    if (currentKind === 'video' && event.target === video) {
      touchX = null;
      return;
    }
    touchX = event.changedTouches[0].clientX;
  }, {passive: true});
  stage.addEventListener('touchend', event => {
    if (touchX === null) return;
    const dx = event.changedTouches[0].clientX - touchX;
    touchX = null;
    if (Math.abs(dx) <= 50) return;
    if (dx < 0 && index < cards.length - 1) show(index + 1);
    if (dx > 0 && index > 0) show(index - 1);
  }, {passive: true});

  window.addEventListener('resize', () => {
    if (!info.hidden) box.style.setProperty('--info-height', info.offsetHeight + 'px');
  });
  window.addEventListener('hashchange', openFromLocation);

  document.addEventListener('keydown', event => {
    if (box.hidden) return;
    if (event.key === 'Escape') {
      close();
      return;
    }
    if (currentKind === 'video' && (event.target === video || document.activeElement === video)) return;
    if (event.key === 'ArrowLeft' && index > 0) show(index - 1);
    if (event.key === 'ArrowRight' && index < cards.length - 1) show(index + 1);
    if (event.key === 'ArrowDown') setInfoVisible(info.hidden);
  });

  openFromLocation();
})();`

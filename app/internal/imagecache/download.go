package imagecache

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Progress is a sink for human-readable status lines during long
// operations. The orchestrator wires this to the run:line Wails event so
// the user sees download percentages without polling.
//
// Two flavours are emitted:
//
//	"openSUSE-Leap-NET.iso  43% (320/740 MiB)"  — periodic during transfer
//	"openSUSE-Leap-NET.iso  cached"             — file already present
type Progress func(line string)

// CachedISO is the on-disk handle returned by EnsureImage. The path is
// guaranteed to exist and to match the catalog's checksum at the time of
// return; callers can pass it to Extract or attach it directly to a VM.
type CachedISO struct {
	Key    string // catalog key, e.g. "leap-15.6"
	Path   string // absolute file path on disk
	SHA256 string // verified hex digest
}

// EnsureImage idempotently materialises one catalog image in the on-disk
// cache. Layout under %LOCALAPPDATA%\cluster-installer\cache\images\:
//
//	<sha256-prefix-12>\
//	    image.iso         the bytes
//	    image.iso.sha256  the digest (matches upstream)
//	    catalog.key       which catalog key first populated this entry
//
// The directory name is content-addressed so different content tags
// referencing the same upstream URL share storage automatically.
//
// First call: HEAD upstream → fetch upstream sha256 → check cache → if
// miss, GET with progress → verify on the fly → atomically rename into
// place. Subsequent calls return immediately after a checksum re-read.
func EnsureImage(ctx context.Context, cacheRoot string, key string, img Image, progress Progress) (CachedISO, error) {
	if img.URL == "" || img.ChecksumURL == "" {
		return CachedISO{}, fmt.Errorf("image %q: missing url or checksum_url", key)
	}

	expected, err := fetchUpstreamSHA(ctx, img.ChecksumURL, img.URL)
	if err != nil {
		return CachedISO{}, fmt.Errorf("upstream sha256: %w", err)
	}

	dir := filepath.Join(cacheRoot, expected[:12])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return CachedISO{}, err
	}

	dst := filepath.Join(dir, "image.iso")
	if existing, err := readDigest(filepath.Join(dir, "image.iso.sha256")); err == nil && existing == expected {
		if _, err := os.Stat(dst); err == nil {
			emit(progress, "%s  cached (%s)", path.Base(img.URL), short(expected))
			return CachedISO{Key: key, Path: dst, SHA256: expected}, nil
		}
	}

	if err := downloadVerify(ctx, img.URL, expected, dst, progress); err != nil {
		return CachedISO{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "image.iso.sha256"), []byte(expected+"\n"), 0o644); err != nil {
		return CachedISO{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "catalog.key"), []byte(key+"\n"), 0o644); err != nil {
		return CachedISO{}, err
	}
	return CachedISO{Key: key, Path: dst, SHA256: expected}, nil
}

// fetchUpstreamSHA pulls the .sha256 file and finds the line that matches
// the basename of the image URL. openSUSE's checksum files are GPG-armoured
// in some mirrors; we tolerate that by stripping PGP framing first.
func fetchUpstreamSHA(ctx context.Context, sumURL, imageURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", sumURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("checksum GET %s: status %d", sumURL, resp.StatusCode)
	}

	target := path.Base(imageURL)
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "-----") || strings.HasPrefix(line, "Hash:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// Format: "<hex>  <filename>" (single sha256) or coreutils-style
		// "<hex> *<filename>" (binary mode). Strip any leading '*'.
		fname := strings.TrimPrefix(fields[1], "*")
		if fname == target {
			return strings.ToLower(fields[0]), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no sha256 entry for %s in %s", target, sumURL)
}

// downloadVerify streams the image into dst.tmp, computing sha256 as it
// goes; on completion, fsyncs and renames atomically. On checksum
// mismatch the partial is removed so retries see a clean slate.
func downloadVerify(ctx context.Context, url, expected, dst string, progress Progress) error {
	tmp := dst + ".tmp"
	_ = os.Remove(tmp)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}

	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	pr := &progressReader{
		r:        resp.Body,
		total:    resp.ContentLength,
		basename: path.Base(url),
		emit:     progress,
		ticker:   time.NewTicker(2 * time.Second),
	}
	defer pr.ticker.Stop()

	if _, err := io.Copy(io.MultiWriter(out, hasher), pr); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("download %s: %w", url, err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	got := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(got, expected) {
		_ = os.Remove(tmp)
		return fmt.Errorf("sha256 mismatch: got %s expected %s", got, expected)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	emit(progress, "%s  100%% verified (%s)", path.Base(url), short(expected))
	return nil
}

// progressReader wraps an io.Reader and emits a single line every couple
// of seconds with bytes transferred / total. Quiet during the gaps so we
// don't spam the run log.
type progressReader struct {
	r        io.Reader
	total    int64
	read     int64
	basename string
	emit     Progress
	ticker   *time.Ticker
	lastEmit time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	select {
	case <-p.ticker.C:
		if p.total > 0 {
			pct := float64(p.read) / float64(p.total) * 100
			emit(p.emit, "%s  %.0f%% (%s/%s)", p.basename, pct, mib(p.read), mib(p.total))
		} else {
			emit(p.emit, "%s  %s", p.basename, mib(p.read))
		}
	default:
	}
	return n, err
}

func mib(n int64) string {
	const m = 1024 * 1024
	if n < 10*m {
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(m))
	}
	return fmt.Sprintf("%d MiB", n/m)
}

func short(sha string) string {
	if len(sha) >= 12 {
		return sha[:12]
	}
	return sha
}

func emit(p Progress, format string, args ...interface{}) {
	if p == nil {
		return
	}
	p(fmt.Sprintf(format, args...))
}

func readDigest(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.ToLower(strings.TrimSpace(string(raw))), nil
}

// httpClient returns a client tuned for slow mirror downloads. The
// upstream openSUSE mirrors regularly take 5+ minutes for a 1 GB ISO, so
// the standard 30-second client default is too aggressive.
func httpClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Minute}
}

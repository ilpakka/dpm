package installer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	pgpopenpgp "github.com/ProtonMail/go-crypto/openpgp"
	pgparmor "github.com/ProtonMail/go-crypto/openpgp/armor"

	"dpm.fi/dpm/internal/adapter"
	"dpm.fi/dpm/internal/catalog"
)

// HTTPBackend downloads a static binary or archive from a URL and verifies
// its SHA-256 digest before handing it to the adapter.
type HTTPBackend struct {
	logger     Logger
	client     *http.Client // used for archive downloads — generous timeout
	metaClient *http.Client // used for small metadata files (PGP keys/sigs)
}

func (h *HTTPBackend) Type() catalog.MethodType { return catalog.MethodHTTP }

func (h *HTTPBackend) Available() bool { return true } // always available

func (h *HTTPBackend) PrepareBundle(tool catalog.Tool, version catalog.ToolVersion, method catalog.InstallMethod, dpmRoot string) (adapter.Bundle, func(), error) {
	if method.URL == "" {
		return adapter.Bundle{}, nil, fmt.Errorf("http: no URL specified for %s@%s", tool.ID, version.Version)
	}

	// Download to cache: ~/.dpm/cache/<toolID>/<version>/
	cacheDir := filepath.Join(dpmRoot, "cache", tool.ID, version.Version)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return adapter.Bundle{}, nil, fmt.Errorf("http: create cache dir: %w", err)
	}

	filename := filepath.Base(method.URL)
	archivePath := filepath.Join(cacheDir, filename)

	// Check if already cached and hash matches.
	if h.cacheValid(archivePath, method.SHA256) {
		h.logger.Printf("http: using cached %s", archivePath)
	} else {
		h.logger.Printf("http: downloading %s", method.URL)
		if err := withRetry(4, h.logger, func() error {
			return h.download(method.URL, archivePath)
		}); err != nil {
			return adapter.Bundle{}, nil, err
		}
	}

	// Integrity check — require at least SHA-256 or PGP; refuse installation otherwise.
	hasPGP := method.PGPKeyURL != "" && method.PGPSigURL != ""
	hasHash := method.SHA256 != "" && !isPlaceholderHash(method.SHA256)

	if !hasHash && !hasPGP {
		_ = os.Remove(archivePath)
		return adapter.Bundle{}, nil, fmt.Errorf(
			"http: refusing to install %s: no SHA-256 hash or PGP signature configured — add a sha256: field to the catalog entry",
			filename,
		)
	}

	verified := false
	if hasHash {
		if err := h.verifySHA256(archivePath, method.SHA256); err != nil {
			_ = os.Remove(archivePath)
			return adapter.Bundle{}, nil, err
		}
		h.logger.Printf("http: SHA-256 verified for %s", filename)
		verified = true
	}

	// PGP verification using the publisher's own public key (optional).
	// Runs after SHA256 so a corrupt download is rejected before PGP is attempted.
	if method.PGPKeyURL != "" && method.PGPSigURL != "" {
		if err := h.verifyPGP(archivePath, method.PGPKeyURL, method.PGPSigURL, cacheDir); err != nil {
			_ = os.Remove(archivePath)
			return adapter.Bundle{}, nil, err
		}
		h.logger.Printf("http: PGP signature verified for %s", filename)
		verified = true
	}

	bundle := adapter.Bundle{
		ToolID:      tool.ID,
		ToolName:    tool.Name,
		Version:     version.Version,
		ArchivePath: archivePath,
		Binaries:    []string{binaryName(tool, method)},
		DataDirs:    method.DataDirs,
		SHA256:      method.SHA256,
		Verified:    verified,
		Method:      string(catalog.MethodHTTP),
	}

	// No cleanup needed — the file stays in cache for future use.
	return bundle, nil, nil
}

func (h *HTTPBackend) download(url, destPath string) error {
	resp, err := h.client.Get(url)
	if err != nil {
		return fmt.Errorf("http: download %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http: download %s: status %d", url, resp.StatusCode)
	}

	tmpPath := destPath + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("http: create temp file: %w", err)
	}

	pw := &progressWriter{
		total:     resp.ContentLength,
		startTime: time.Now(),
		logger:    h.logger,
	}
	tee := io.TeeReader(resp.Body, pw)

	if _, err := io.Copy(f, tee); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("http: write %s: %w", destPath, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("http: close %s: %w", destPath, err)
	}

	// Atomic rename.
	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("http: rename %s: %w", destPath, err)
	}

	return nil
}

func (h *HTTPBackend) verifySHA256(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("http: open for sha256: %w", err)
	}
	defer f.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return fmt.Errorf("http: hash %s: %w", path, err)
	}

	actual := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("http: SHA-256 mismatch for %s: expected %s, got %s", filepath.Base(path), expected, actual)
	}
	return nil
}

func (h *HTTPBackend) cacheValid(path, expectedSHA256 string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	if expectedSHA256 == "" || isPlaceholderHash(expectedSHA256) {
		return false // Can't verify — re-download.
	}
	return h.verifySHA256(path, expectedSHA256) == nil
}

// zeroHash is the all-zeros SHA-256 placeholder used in development catalog entries.
const zeroHash = "0000000000000000000000000000000000000000000000000000000000000000"

// isPlaceholderHash returns true for all-zeros or empty hashes used in development.
func isPlaceholderHash(hash string) bool {
	return hash == "" || hash == zeroHash
}

// withRetry calls fn up to maxAttempts times, sleeping with exponential backoff
// between failures (1s, 2s, 4s, …). Returns the last error if all attempts fail.
func withRetry(maxAttempts int, logger Logger, fn func() error) error {
	var err error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err = fn(); err == nil {
			return nil
		}
		if attempt < maxAttempts-1 {
			wait := time.Duration(1<<uint(attempt)) * time.Second
			logger.Printf("http: attempt %d/%d failed: %v — retrying in %v", attempt+1, maxAttempts, err, wait)
			time.Sleep(wait)
		}
	}
	return fmt.Errorf("http: all %d attempts failed: %w", maxAttempts, err)
}

// progressWriter tracks download progress and logs periodic updates.
// It implements io.Writer and is intended for use with io.TeeReader.
type progressWriter struct {
	written   int64
	total     int64 // -1 if Content-Length is unknown
	startTime time.Time
	logger    Logger
	lastLog   int64
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	pw.written += int64(n)

	// Log every 512 KB, or every 10% of total if total is known and larger.
	logInterval := int64(512 * 1024)
	if pw.total > 0 && pw.total/10 > logInterval {
		logInterval = pw.total / 10
	}

	done := pw.total > 0 && pw.written >= pw.total
	if done || pw.written-pw.lastLog >= logInterval {
		pw.lastLog = pw.written

		elapsed := time.Since(pw.startTime).Seconds()
		if elapsed < 0.001 {
			elapsed = 0.001
		}
		speed := float64(pw.written) / elapsed // bytes/sec

		if pw.total > 0 {
			pct := float64(pw.written) / float64(pw.total) * 100
			eta := ""
			if speed > 0 && !done {
				eta = fmt.Sprintf(" ETA %.0fs", float64(pw.total-pw.written)/speed)
			}
			pw.logger.Printf("http: %.0f%% (%s / %s) %.1f KB/s%s",
				pct, formatBytes(pw.written), formatBytes(pw.total), speed/1024, eta)
		} else {
			pw.logger.Printf("http: downloaded %s (%.1f KB/s)", formatBytes(pw.written), speed/1024)
		}
	}
	return n, nil
}

// formatBytes formats a byte count as a human-readable string.
func formatBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}

// ---------------------------------------------------------------------------
// PGP verification — uses the tool publisher's own public key
// ---------------------------------------------------------------------------

// verifyPGP fetches the publisher's ASCII-armored public key and detached
// signature, then verifies that archivePath was signed by that key.
// The key and signature are cached in cacheDir so they are only fetched once
// per tool version.
func (h *HTTPBackend) verifyPGP(archivePath, keyURL, sigURL, cacheDir string) error {
	keyPath := filepath.Join(cacheDir, "publisher.asc")
	sigPath := filepath.Join(cacheDir, "archive.sig")

	// Download the public key (cache indefinitely — keys don't change for a release).
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		h.logger.Printf("http: fetching PGP public key from %s", keyURL)
		if err := h.fetchToFile(keyURL, keyPath); err != nil {
			return fmt.Errorf("http: fetch PGP key: %w", err)
		}
	}

	// Always re-fetch the signature (it is release-specific and small).
	h.logger.Printf("http: fetching PGP signature from %s", sigURL)
	if err := h.fetchToFile(sigURL, sigPath); err != nil {
		return fmt.Errorf("http: fetch PGP signature: %w", err)
	}

	// Read the keyring.
	keyFile, err := os.Open(keyPath)
	if err != nil {
		return fmt.Errorf("http: open PGP key: %w", err)
	}
	defer keyFile.Close()

	// Keys may be binary or ASCII-armored — try armored first, fall back to binary.
	var keyring pgpopenpgp.EntityList
	armorBlock, armorErr := pgparmor.Decode(keyFile)
	if armorErr == nil {
		keyring, err = pgpopenpgp.ReadKeyRing(armorBlock.Body)
	} else {
		_, _ = keyFile.Seek(0, io.SeekStart)
		keyring, err = pgpopenpgp.ReadKeyRing(keyFile)
	}
	if err != nil {
		return fmt.Errorf("http: parse PGP key: %w", err)
	}

	// Open the signature.
	sigFile, err := os.Open(sigPath)
	if err != nil {
		return fmt.Errorf("http: open PGP signature: %w", err)
	}
	defer sigFile.Close()

	// Open the archive for verification.
	archiveFile, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("http: open archive for PGP check: %w", err)
	}
	defer archiveFile.Close()

	// Verify the detached signature.
	// pgpopenpgp.CheckDetachedSignature handles both binary and ASCII-armored sigs.
	if _, err := pgpopenpgp.CheckDetachedSignature(keyring, archiveFile, sigFile, nil); err != nil {
		return fmt.Errorf("http: PGP signature invalid for %s: %w", filepath.Base(archivePath), err)
	}
	return nil
}

// fetchToFile downloads url and writes it to destPath atomically.
func (h *HTTPBackend) fetchToFile(url, destPath string) error {
	resp, err := h.metaClient.Get(url)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}

	tmp := destPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("fetchToFile: create %q: %w", tmp, err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("fetchToFile: write %q: %w", destPath, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("fetchToFile: close %q: %w", tmp, err)
	}
	return os.Rename(tmp, destPath)
}

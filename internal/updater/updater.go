package updater

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/compozy/cc-loop/internal/installer"
	"github.com/compozy/cc-loop/internal/loop"
)

const (
	defaultOwner       = "compozy"
	defaultRepo        = "cc-loop"
	defaultAPIBaseURL  = "https://api.github.com"
	defaultMarketplace = "cc-loop-plugins"
)

type Options struct {
	Version          string
	Owner            string
	Repo             string
	APIBaseURL       string
	HTTPClient       *http.Client
	TargetBinary     string
	ClaudeBinary     string
	GOOS             string
	GOARCH           string
	SkipMarketplace  bool
	SkipSelfUpdate   bool
	KeepDownloadRoot string
}

type releaseResponse struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Upgrade installs a GitHub release binary, refreshes the managed runtime, and
// asks Claude Code to refresh the plugin marketplace when Claude Code is available.
func Upgrade(ctx context.Context, paths loop.Paths, opts Options) ([]string, error) {
	cfg, err := normalizeOptions(opts)
	if err != nil {
		return nil, err
	}

	release, err := fetchRelease(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if release.TagName == "" {
		return nil, fmt.Errorf("release response missing tag_name")
	}
	version := strings.TrimPrefix(release.TagName, "v")
	assetName, binaryName, err := releaseArchiveName(version, cfg.GOOS, cfg.GOARCH)
	if err != nil {
		return nil, err
	}

	archiveAsset, ok := findAsset(release.Assets, assetName)
	if !ok {
		return nil, fmt.Errorf("release %s does not include asset %q", release.TagName, assetName)
	}
	checksumsAsset, ok := findAsset(release.Assets, "checksums.txt")
	if !ok {
		return nil, fmt.Errorf("release %s does not include checksums.txt", release.TagName)
	}

	downloadRoot, cleanup, err := prepareDownloadRoot(cfg.KeepDownloadRoot)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	archivePath := filepath.Join(downloadRoot, archiveAsset.Name)
	if err := downloadFile(ctx, cfg.HTTPClient, archiveAsset.BrowserDownloadURL, archivePath); err != nil {
		return nil, err
	}
	checksumsPath := filepath.Join(downloadRoot, checksumsAsset.Name)
	if err := downloadFile(ctx, cfg.HTTPClient, checksumsAsset.BrowserDownloadURL, checksumsPath); err != nil {
		return nil, err
	}
	if err := verifyChecksum(checksumsPath, archiveAsset.Name, archivePath); err != nil {
		return nil, err
	}

	extractedBinary, err := extractReleaseBinary(archivePath, binaryName, downloadRoot)
	if err != nil {
		return nil, err
	}

	messages := []string{
		fmt.Sprintf("Downloaded cc-loop %s from %s", release.TagName, archiveAsset.BrowserDownloadURL),
		fmt.Sprintf("Verified checksum for %s", archiveAsset.Name),
	}

	if !cfg.SkipSelfUpdate {
		if err := replaceBinary(extractedBinary, cfg.TargetBinary); err != nil {
			return nil, err
		}
		messages = append(messages, fmt.Sprintf("Updated CLI binary at %s", cfg.TargetBinary))
	}

	installMessages, err := installer.Install(paths, installer.Options{SourceBinary: extractedBinary})
	if err != nil {
		return nil, err
	}
	messages = append(messages, installMessages...)

	marketplaceMessages, err := refreshMarketplace(ctx, cfg, release.TagName)
	if err != nil {
		return nil, err
	}
	messages = append(messages, marketplaceMessages...)
	return messages, nil
}

func normalizeOptions(opts Options) (Options, error) {
	if opts.Owner == "" {
		opts.Owner = defaultOwner
	}
	if opts.Repo == "" {
		opts.Repo = defaultRepo
	}
	if opts.APIBaseURL == "" {
		opts.APIBaseURL = defaultAPIBaseURL
	}
	opts.APIBaseURL = strings.TrimRight(opts.APIBaseURL, "/")
	if opts.HTTPClient == nil {
		opts.HTTPClient = http.DefaultClient
	}
	if opts.GOOS == "" {
		opts.GOOS = runtime.GOOS
	}
	if opts.GOARCH == "" {
		opts.GOARCH = runtime.GOARCH
	}
	if opts.Version == "" {
		opts.Version = "latest"
	}
	if !opts.SkipSelfUpdate && opts.TargetBinary == "" {
		executable, err := os.Executable()
		if err != nil {
			return Options{}, fmt.Errorf("resolve current executable: %w", err)
		}
		opts.TargetBinary = executable
	}
	return opts, nil
}

func fetchRelease(ctx context.Context, opts Options) (releaseResponse, error) {
	endpoint := opts.APIBaseURL + "/repos/" + url.PathEscape(opts.Owner) + "/" + url.PathEscape(opts.Repo) + "/releases"
	if opts.Version == "latest" {
		endpoint += "/latest"
	} else {
		endpoint += "/tags/" + url.PathEscape(normalizeTag(opts.Version))
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return releaseResponse{}, fmt.Errorf("create release request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := opts.HTTPClient.Do(request)
	if err != nil {
		return releaseResponse{}, fmt.Errorf("fetch release metadata: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
		if readErr != nil {
			return releaseResponse{}, fmt.Errorf("read release error body: %w", readErr)
		}
		return releaseResponse{}, fmt.Errorf("fetch release metadata: %s: %s", response.Status, strings.TrimSpace(string(body)))
	}

	var release releaseResponse
	if err := json.NewDecoder(response.Body).Decode(&release); err != nil {
		return releaseResponse{}, fmt.Errorf("decode release metadata: %w", err)
	}
	return release, nil
}

func normalizeTag(version string) string {
	if strings.HasPrefix(version, "v") {
		return version
	}
	return "v" + version
}

func findAsset(assets []releaseAsset, name string) (releaseAsset, bool) {
	for _, asset := range assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return releaseAsset{}, false
}

func prepareDownloadRoot(configured string) (string, func(), error) {
	if configured != "" {
		if err := os.MkdirAll(configured, 0o755); err != nil {
			return "", nil, fmt.Errorf("create download directory: %w", err)
		}
		return configured, func() {}, nil
	}
	dir, err := os.MkdirTemp("", "cc-loop-upgrade-*")
	if err != nil {
		return "", nil, fmt.Errorf("create download directory: %w", err)
	}
	return dir, func() {
		// Best-effort cleanup for a transient download directory.
		_ = os.RemoveAll(dir)
	}, nil
}

func downloadFile(ctx context.Context, client *http.Client, sourceURL string, destination string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return fmt.Errorf("create download request for %s: %w", sourceURL, err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download %s: %w", sourceURL, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
		if readErr != nil {
			return fmt.Errorf("read download error body: %w", readErr)
		}
		return fmt.Errorf("download %s: %s: %s", sourceURL, response.Status, strings.TrimSpace(string(body)))
	}

	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create download destination directory: %w", err)
	}
	file, err := os.Create(destination)
	if err != nil {
		return fmt.Errorf("create download destination %q: %w", destination, err)
	}
	if _, err := io.Copy(file, response.Body); err != nil {
		closeErr := file.Close()
		if closeErr != nil {
			return fmt.Errorf("copy download to %q: %w; close destination: %v", destination, err, closeErr)
		}
		return fmt.Errorf("copy download to %q: %w", destination, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close download destination %q: %w", destination, err)
	}
	return nil
}

func verifyChecksum(checksumsPath string, assetName string, archivePath string) error {
	expected, err := readExpectedChecksum(checksumsPath, assetName)
	if err != nil {
		return err
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive for checksum: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("hash archive %q: %w", archivePath, err)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", assetName, expected, actual)
	}
	return nil
}

func readExpectedChecksum(checksumsPath string, assetName string) (string, error) {
	content, err := os.ReadFile(checksumsPath)
	if err != nil {
		return "", fmt.Errorf("read checksums %q: %w", checksumsPath, err)
	}
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if name == assetName {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksums.txt does not contain %s", assetName)
}

func extractReleaseBinary(archivePath string, binaryName string, destinationRoot string) (string, error) {
	destination := filepath.Join(destinationRoot, "extracted", binaryName)
	if strings.HasSuffix(archivePath, ".zip") {
		return extractZipBinary(archivePath, binaryName, destination)
	}
	return extractTarGzipBinary(archivePath, binaryName, destination)
}

func extractTarGzipBinary(archivePath string, binaryName string, destination string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("open archive %q: %w", archivePath, err)
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("open gzip archive %q: %w", archivePath, err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read tar archive %q: %w", archivePath, err)
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != binaryName {
			continue
		}
		if err := writeExtractedBinary(destination, tarReader); err != nil {
			return "", err
		}
		return destination, nil
	}
	return "", fmt.Errorf("archive %q does not contain %s", archivePath, binaryName)
}

func extractZipBinary(archivePath string, binaryName string, destination string) (string, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("open zip archive %q: %w", archivePath, err)
	}
	defer reader.Close()

	for _, file := range reader.File {
		if file.FileInfo().IsDir() || filepath.Base(file.Name) != binaryName {
			continue
		}
		source, err := file.Open()
		if err != nil {
			return "", fmt.Errorf("open %s in %q: %w", file.Name, archivePath, err)
		}
		if err := writeExtractedBinary(destination, source); err != nil {
			closeErr := source.Close()
			if closeErr != nil {
				return "", fmt.Errorf("extract %s from %q: %w; close source: %v", file.Name, archivePath, err, closeErr)
			}
			return "", err
		}
		if err := source.Close(); err != nil {
			return "", fmt.Errorf("close %s in %q: %w", file.Name, archivePath, err)
		}
		return destination, nil
	}
	return "", fmt.Errorf("archive %q does not contain %s", archivePath, binaryName)
}

func writeExtractedBinary(destination string, source io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create extracted binary directory: %w", err)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return fmt.Errorf("create extracted binary %q: %w", destination, err)
	}
	if _, err := io.Copy(file, source); err != nil {
		closeErr := file.Close()
		if closeErr != nil {
			return fmt.Errorf("write extracted binary %q: %w; close destination: %v", destination, err, closeErr)
		}
		return fmt.Errorf("write extracted binary %q: %w", destination, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close extracted binary %q: %w", destination, err)
	}
	return nil
}

func replaceBinary(source string, destination string) error {
	if destination == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create CLI binary directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), ".cc-loop-*")
	if err != nil {
		return fmt.Errorf("create temp CLI binary: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)

	sourceFile, err := os.Open(source)
	if err != nil {
		closeErr := temp.Close()
		if closeErr != nil {
			return fmt.Errorf("open downloaded binary %q: %w; close temp: %v", source, err, closeErr)
		}
		return fmt.Errorf("open downloaded binary %q: %w", source, err)
	}
	if _, err := io.Copy(temp, sourceFile); err != nil {
		closeSourceErr := sourceFile.Close()
		closeTempErr := temp.Close()
		if closeSourceErr != nil || closeTempErr != nil {
			return fmt.Errorf("copy downloaded binary to temp: %w; close source: %v; close temp: %v", err, closeSourceErr, closeTempErr)
		}
		return fmt.Errorf("copy downloaded binary to temp: %w", err)
	}
	if err := sourceFile.Close(); err != nil {
		closeErr := temp.Close()
		if closeErr != nil {
			return fmt.Errorf("close downloaded binary %q: %w; close temp: %v", source, err, closeErr)
		}
		return fmt.Errorf("close downloaded binary %q: %w", source, err)
	}
	if err := temp.Chmod(0o755); err != nil {
		closeErr := temp.Close()
		if closeErr != nil {
			return fmt.Errorf("chmod temp CLI binary: %w; close temp: %v", err, closeErr)
		}
		return fmt.Errorf("chmod temp CLI binary: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temp CLI binary: %w", err)
	}
	if err := os.Rename(tempName, destination); err != nil {
		return fmt.Errorf("replace CLI binary %q: %w", destination, err)
	}
	return nil
}

func refreshMarketplace(ctx context.Context, opts Options, tag string) ([]string, error) {
	if opts.SkipMarketplace {
		source := opts.Owner + "/" + opts.Repo + "@" + tag
		return []string{
			"Skipped Claude Code plugin marketplace refresh.",
			fmt.Sprintf("To refresh it manually, run: claude plugin marketplace add %s", source),
			fmt.Sprintf("Then run: claude plugin marketplace update %s", defaultMarketplace),
		}, nil
	}

	claudeBinary := opts.ClaudeBinary
	if claudeBinary == "" {
		resolved, err := exec.LookPath("claude")
		if err != nil {
			source := opts.Owner + "/" + opts.Repo + "@" + tag
			return []string{
				"Claude Code CLI was not found on PATH; skipped plugin marketplace refresh.",
				fmt.Sprintf("Run manually when Claude Code is available: claude plugin marketplace add %s", source),
				fmt.Sprintf("Then run: claude plugin marketplace update %s", defaultMarketplace),
			}, nil
		}
		claudeBinary = resolved
	}

	source := opts.Owner + "/" + opts.Repo + "@" + tag
	replacedMarketplace := false
	if err := runCommand(ctx, claudeBinary, "plugin", "marketplace", "add", source); err != nil {
		if !isDifferentSourceMarketplaceError(err) {
			return nil, err
		}
		if err := runCommand(ctx, claudeBinary, "plugin", "marketplace", "remove", defaultMarketplace); err != nil {
			return nil, err
		}
		if err := runCommand(ctx, claudeBinary, "plugin", "marketplace", "add", source); err != nil {
			return nil, err
		}
		replacedMarketplace = true
	}
	if err := runCommand(ctx, claudeBinary, "plugin", "marketplace", "update", defaultMarketplace); err != nil {
		return nil, err
	}
	if replacedMarketplace {
		return []string{
			fmt.Sprintf("Replaced existing Claude Code plugin marketplace %s with %s.", defaultMarketplace, source),
			"Restart Claude Code so plugin hooks and skills reload.",
		}, nil
	}
	return []string{
		fmt.Sprintf("Refreshed Claude Code plugin marketplace %s at %s.", defaultMarketplace, tag),
		"Restart Claude Code so plugin hooks and skills reload.",
	}, nil
}

func runCommand(ctx context.Context, command string, args ...string) error {
	cmd := exec.CommandContext(ctx, command, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run %s %s: %w: %s", command, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func isDifferentSourceMarketplaceError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "marketplace '"+defaultMarketplace+"' is already added from a different source") ||
		strings.Contains(message, "already added from a different source")
}

func releaseArchiveName(version string, goos string, goarch string) (string, string, error) {
	if goos == "" || goarch == "" {
		return "", "", fmt.Errorf("goos and goarch are required")
	}
	archiveArch, err := releaseArch(goarch)
	if err != nil {
		return "", "", err
	}
	binaryName := "cc-loop"
	extension := ".tar.gz"
	if goos == "windows" {
		binaryName += ".exe"
		extension = ".zip"
	}
	return fmt.Sprintf("cc-loop_%s_%s_%s%s", version, goos, archiveArch, extension), binaryName, nil
}

func releaseArch(goarch string) (string, error) {
	switch goarch {
	case "amd64":
		return "x86_64", nil
	case "386":
		return "i386", nil
	case "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported architecture %q", goarch)
	}
}

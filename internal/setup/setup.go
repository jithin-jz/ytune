// Package setup finds, installs and updates the programs ytune depends on.
//
// On Windows ytune downloads its own copies on first run, so nothing has to be
// installed by hand and every PC runs the same versions. Elsewhere it uses the
// programs already on PATH.
package setup

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// updateInterval is how often ytune looks for a new yt-dlp. YouTube changes
// often and a stale yt-dlp is the most likely reason for songs to stop loading.
const updateInterval = 24 * time.Hour

const (
	ytDlpDir     = "yt-dlp"
	ytDlpExe     = "yt-dlp.exe"
	ytDlpArchive = "yt-dlp_win.zip"
	ytDlpSizeMB  = 23
	ytDlpLatest  = "/yt-dlp/yt-dlp/releases/latest"
	ytDlpRelease = "/yt-dlp/yt-dlp/releases/download/"
	ytDlpSums    = "SHA2-256SUMS"
	lastCheck    = "last-check"
	tempPrefix   = ".tmp-"
)

// pinnedTool is a program installed at one exact version, checked against a
// hash kept here in the source. New versions arrive with new ytune releases.
type pinnedTool struct {
	dir     string
	version string
	path    string
	sha256  string
	exe     string
	sizeMB  int
}

var (
	// This is the MinGW build because the MSVC build needs the Visual C++
	// runtime, which a clean copy of Windows may not have.
	mpvTool = pinnedTool{
		dir:     "mpv",
		version: "v0.42.0",
		path:    "/mpv-player/mpv/releases/download/v0.42.0/mpv-v0.42.0-x86_64-w64-mingw32-full.zip",
		sha256:  "c7d4ff6a67dd33659f71d81efa56335fea2213c7522fd67235f3abf0d9ea1dbd",
		exe:     "mpv.exe",
		sizeMB:  62,
	}
	denoTool = pinnedTool{
		dir:     "deno",
		version: "v2.9.8",
		path:    "/denoland/deno/releases/download/v2.9.8/deno-x86_64-pc-windows-msvc.zip",
		sha256:  "cba84b26e0df5ae7f74fc66b86a95b866872a86292ebd84fa1e959927edd93bf",
		exe:     "deno.exe",
		sizeMB:  41,
	}
)

// ytDlpVersion matches release names such as 2026.10.26 or 2026.10.26.1.
var ytDlpVersion = regexp.MustCompile(`^\d{4}\.\d{2}\.\d{2}(\.\d+)?$`)

var (
	// baseURL is where every download comes from. Tests point it elsewhere.
	baseURL = "https://github.com"
	// UserAgent identifies ytune to GitHub.
	UserAgent = "ytune"
)

// Tools says where ytune's helper programs are.
type Tools struct {
	Mpv       string
	JSRuntime string // In the form yt-dlp's --js-runtimes option expects.

	home      string // Where ytune keeps its own copies, if it does.
	ytDlpPath string // Set when yt-dlp comes from PATH instead.
}

// YtDlp returns the yt-dlp to run. It's a method because an update can install
// a newer version while ytune is open.
func (t *Tools) YtDlp() string {
	if t.ytDlpPath != "" {
		return t.ytDlpPath
	}
	return filepath.Join(t.home, ytDlpDir, installedYtDlp(t.home), ytDlpExe)
}

// Ensure returns the tools, installing them first if any are missing.
func Ensure(ctx context.Context, in io.Reader, out io.Writer) (*Tools, error) {
	if runtime.GOOS != "windows" {
		return fromPath()
	}
	home, err := homeDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return nil, fmt.Errorf("could not create %s: %w", home, err)
	}
	removeTempDirs(home)
	if err := installMissing(ctx, home, in, out); err != nil {
		return nil, err
	}
	return &Tools{
		home:      home,
		Mpv:       mpvTool.exePath(home),
		JSRuntime: "deno:" + denoTool.exePath(home),
	}, nil
}

func homeDir() (string, error) {
	if home := os.Getenv("YTUNE_HOME"); home != "" {
		return home, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("could not find a folder for ytune's programs: %w", err)
	}
	return filepath.Join(cache, "ytune"), nil
}

func fromPath() (*Tools, error) {
	mpv, mpvErr := exec.LookPath("mpv")
	ytDlp, ytDlpErr := exec.LookPath("yt-dlp")
	var jsRuntime string
	for _, name := range []string{"deno", "node", "bun"} {
		if path, err := exec.LookPath(name); err == nil {
			jsRuntime = name + ":" + path
			break
		}
	}
	var missing []string
	if mpvErr != nil {
		missing = append(missing, "mpv")
	}
	if ytDlpErr != nil {
		missing = append(missing, "yt-dlp")
	}
	if jsRuntime == "" {
		missing = append(missing, "Deno, Node.js or Bun")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("please install these first: %s", strings.Join(missing, ", "))
	}
	return &Tools{Mpv: mpv, JSRuntime: jsRuntime, ytDlpPath: ytDlp}, nil
}

type step struct {
	name    string
	purpose string
	sizeMB  int
	install func() error
}

func installMissing(ctx context.Context, home string, in io.Reader, out io.Writer) error {
	var steps []step
	if installedYtDlp(home) == "" {
		steps = append(steps, step{"yt-dlp", "finds and downloads music from YouTube", ytDlpSizeMB, func() error {
			version, sum, err := latestYtDlp(ctx)
			if err != nil {
				return err
			}
			return installYtDlp(ctx, home, version, sum, out)
		}})
	}
	for _, tool := range []struct {
		pinnedTool
		name, purpose string
	}{
		{mpvTool, "mpv", "plays the audio"},
		{denoTool, "Deno", "lets yt-dlp get past YouTube's download checks"},
	} {
		if !exists(tool.exePath(home)) {
			steps = append(steps, step{tool.name, tool.purpose, tool.sizeMB, func() error {
				return tool.install(ctx, home, tool.name, out)
			}})
		}
	}
	if len(steps) == 0 {
		return nil
	}

	total := 0
	fmt.Fprintln(out, "ytune needs these programs to run:")
	fmt.Fprintln(out)
	for _, s := range steps {
		fmt.Fprintf(out, "  %-7s %s\n", s.name, s.purpose)
		total += s.sizeMB
	}
	fmt.Fprintf(out, "\nThis is a one-time download of about %d MB, into:\n  %s\n\n", total, home)
	if !confirm(in, out) {
		return errors.New("setup cancelled, so ytune can't start")
	}
	for _, s := range steps {
		if err := s.install(); err != nil {
			return fmt.Errorf("could not install %s: %w", s.name, err)
		}
	}
	fmt.Fprintln(out, "\nSetup finished.")
	return nil
}

func confirm(in io.Reader, out io.Writer) bool {
	fmt.Fprint(out, "Download now? [Y/n] ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false // Nobody is there to answer.
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "" || answer == "y" || answer == "yes"
}

func (t pinnedTool) exePath(home string) string {
	return filepath.Join(home, t.dir, t.version, t.exe)
}

func (t pinnedTool) install(ctx context.Context, home, name string, out io.Writer) error {
	dest := filepath.Join(home, t.dir, t.version)
	if err := installArchive(ctx, home, name, baseURL+t.path, t.sha256, t.exe, dest, out); err != nil {
		return err
	}
	removeOtherVersions(filepath.Join(home, t.dir), t.version)
	return nil
}

// installedYtDlp returns the newest installed version, or "". Version names are
// dates padded with zeros, so comparing them as text puts them in order.
func installedYtDlp(home string) string {
	entries, err := os.ReadDir(filepath.Join(home, ytDlpDir))
	if err != nil {
		return ""
	}
	newest := ""
	for _, entry := range entries {
		name := entry.Name()
		if name > newest && exists(filepath.Join(home, ytDlpDir, name, ytDlpExe)) {
			newest = name
		}
	}
	return newest
}

// latestYtDlp finds the newest release and the checksum of its archive.
func latestYtDlp(ctx context.Context) (version, sum string, err error) {
	version, err = latestYtDlpVersion(ctx)
	if err != nil {
		return "", "", err
	}
	body, err := getSmall(ctx, baseURL+ytDlpRelease+version+"/"+ytDlpSums)
	if err != nil {
		return "", "", err
	}
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == ytDlpArchive {
			return version, fields[0], nil
		}
	}
	return "", "", fmt.Errorf("no checksum published for %s", ytDlpArchive)
}

// latestYtDlpVersion reads the version from the redirect GitHub answers with,
// which saves using its rate-limited API. Only that first redirect will do:
// downloads redirect again, to servers whose addresses don't include it.
func latestYtDlpVersion(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, baseURL+ytDlpLatest, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", UserAgent)
	client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	location := response.Header.Get("Location")
	_, version, _ := strings.Cut(location, "/releases/tag/")
	// The version becomes a folder name and is compared as text, so it has to
	// look exactly as expected.
	if !ytDlpVersion.MatchString(version) {
		return "", fmt.Errorf("could not read the latest yt-dlp version from %q", location)
	}
	return version, nil
}

func installYtDlp(ctx context.Context, home, version, sum string, out io.Writer) error {
	url := baseURL + ytDlpRelease + version + "/" + ytDlpArchive
	dest := filepath.Join(home, ytDlpDir, version)
	if err := installArchive(ctx, home, "yt-dlp", url, sum, "", dest, out); err != nil {
		return err
	}
	return touch(filepath.Join(home, ytDlpDir, lastCheck))
}

// Update installs a newer yt-dlp if there is one, at most once per
// updateInterval, and returns the new version. A new version goes in its own
// folder, so it can't disturb a copy that's running right now. The old folder
// can't be deleted while it's in use, so it may linger until the next update.
func (t *Tools) Update(ctx context.Context) (string, error) {
	if t.home == "" {
		return "", nil
	}
	marker := filepath.Join(t.home, ytDlpDir, lastCheck)
	if info, err := os.Stat(marker); err == nil && time.Since(info.ModTime()) < updateInterval {
		return "", nil
	}
	version, sum, err := latestYtDlp(ctx)
	if err != nil {
		return "", err
	}
	current := installedYtDlp(t.home)
	if version <= current {
		removeOtherVersions(filepath.Join(t.home, ytDlpDir), current)
		return "", touch(marker)
	}
	if err := installYtDlp(ctx, t.home, version, sum, io.Discard); err != nil {
		return "", err
	}
	removeOtherVersions(filepath.Join(t.home, ytDlpDir), version)
	return version, nil
}

// installArchive downloads a zip, verifies it and unpacks it to dest. Everything
// happens in a temporary folder that's moved into place at the end, so an
// interrupted install never looks like a finished one. If only is set, just that
// file is taken from the archive.
func installArchive(ctx context.Context, home, name, url, sum, only, dest string, out io.Writer) error {
	temp, err := os.MkdirTemp(home, tempPrefix)
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)

	archive := filepath.Join(temp, "download.zip")
	if err := download(ctx, name, url, sum, archive, out); err != nil {
		return err
	}
	unpacked := filepath.Join(temp, "unpacked")
	if err := unzip(archive, unpacked, only); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.Rename(unpacked, dest)
}

func newRequest(ctx context.Context, url string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", UserAgent)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("%s returned %s", url, response.Status)
	}
	return response, nil
}

func getSmall(ctx context.Context, url string) (string, error) {
	response, err := newRequest(ctx, url)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	return string(data), err
}

func download(ctx context.Context, name, url, sum, path string, out io.Writer) error {
	response, err := newRequest(ctx, url)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	hash := sha256.New()
	counter := &progress{name: name, total: response.ContentLength, out: out}
	if _, err := io.Copy(io.MultiWriter(file, hash, counter), response.Body); err != nil {
		return err
	}
	counter.finish()
	if actual := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(actual, sum) {
		return fmt.Errorf("the download failed its security check (expected SHA-256 %s, got %s)", sum, actual)
	}
	return nil
}

// progress prints a percentage on one line as bytes pass through it.
type progress struct {
	name    string
	total   int64
	written int64
	shown   int64
	out     io.Writer
}

func (p *progress) Write(data []byte) (int, error) {
	p.written += int64(len(data))
	if p.total > 0 {
		if percent := p.written * 100 / p.total; percent != p.shown {
			p.shown = percent
			fmt.Fprintf(p.out, "\r  %-7s %3d%%", p.name, percent)
		}
	}
	return len(data), nil
}

func (p *progress) finish() {
	fmt.Fprintf(p.out, "\r  %-7s done\n", p.name)
}

func unzip(archive, dest, only string) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("could not open the download: %w", err)
	}
	defer reader.Close()

	found := false
	for _, entry := range reader.File {
		name := entry.Name
		if only != "" {
			if entry.FileInfo().IsDir() || filepath.Base(filepath.FromSlash(name)) != only {
				continue
			}
			name, found = only, true
		}
		// Refuses entries such as ../../evil.exe that would land outside dest.
		if !filepath.IsLocal(filepath.FromSlash(name)) {
			return fmt.Errorf("the download contains an unsafe path: %s", entry.Name)
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := extractFile(entry, target); err != nil {
			return err
		}
	}
	if only != "" && !found {
		return fmt.Errorf("%s is missing from the download", only)
	}
	return nil
}

func extractFile(entry *zip.File, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	source, err := entry.Open()
	if err != nil {
		return err
	}
	defer source.Close()
	file, err := os.Create(target)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(file, source)
	return err
}

func removeOtherVersions(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != keep {
			os.RemoveAll(filepath.Join(dir, entry.Name())) // Fails if still running.
		}
	}
}

func removeTempDirs(home string) {
	leftovers, _ := filepath.Glob(filepath.Join(home, tempPrefix+"*"))
	for _, path := range leftovers {
		os.RemoveAll(path)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func touch(path string) error {
	now := time.Now()
	if err := os.Chtimes(path, now, now); err == nil {
		return nil
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	return file.Close()
}

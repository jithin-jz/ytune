package setup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testMpvPath  = "/mpv.zip"
	testDenoPath = "/deno.zip"
	releases     = ytDlpRelease
	cdnPath      = "/github-production-release-asset/307260205/"
)

// fakeGitHub answers like the real thing: the latest release is a redirect to
// its tag page, and every download redirects again to a server whose address
// has nothing to do with the version.
type fakeGitHub struct {
	latest   string // The yt-dlp version offered as latest.
	files    map[string][]byte
	requests atomic.Int32
	broken   atomic.Bool
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		file.Write([]byte(content))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (g *fakeGitHub) addYtDlp(t *testing.T, version string) {
	t.Helper()
	data := makeZip(t, map[string]string{
		"yt-dlp.exe":         "yt-dlp " + version,
		"_internal/base.dll": "support file",
		"_internal/":         "",
	})
	g.files[releases+version+"/"+ytDlpArchive] = data
	g.setSums(version, fmt.Sprintf("%s  yt-dlp.exe\n%s  %s\n", strings.Repeat("0", 64), hashOf(data), ytDlpArchive))
}

func (g *fakeGitHub) setSums(version, sums string) {
	g.files[releases+version+"/"+ytDlpSums] = []byte(sums)
}

func (g *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.requests.Add(1)
	if g.broken.Load() {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	switch {
	case r.URL.Path == ytDlpLatest:
		http.Redirect(w, r, "/yt-dlp/yt-dlp/releases/tag/"+g.latest, http.StatusFound)
	case strings.HasPrefix(r.URL.Path, cdnPath):
		data, ok := g.files[r.URL.Query().Get("file")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	default:
		if _, ok := g.files[r.URL.Path]; !ok {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, cdnPath+"abc?file="+url.QueryEscape(r.URL.Path), http.StatusFound)
	}
}

// startGitHub points setup at a fake GitHub and gives it an empty home folder.
func startGitHub(t *testing.T) (*fakeGitHub, string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("automatic setup only runs on Windows")
	}
	mpvData := makeZip(t, map[string]string{"mpv.exe": "mpv", "mpv.com": "wrapper", "extra/notes.txt": "x"})
	denoData := makeZip(t, map[string]string{"deno.exe": "deno"})
	github := &fakeGitHub{
		latest: "2026.10.26",
		files:  map[string][]byte{testMpvPath: mpvData, testDenoPath: denoData},
	}
	github.addYtDlp(t, "2026.10.26")
	server := httptest.NewServer(github)
	t.Cleanup(server.Close)

	oldBase, oldMpv, oldDeno := baseURL, mpvTool, denoTool
	t.Cleanup(func() { baseURL, mpvTool, denoTool = oldBase, oldMpv, oldDeno })
	baseURL = server.URL
	mpvTool.path, mpvTool.sha256 = testMpvPath, hashOf(mpvData)
	denoTool.path, denoTool.sha256 = testDenoPath, hashOf(denoData)

	home := filepath.Join(t.TempDir(), "ytune home")
	t.Setenv("YTUNE_HOME", home)
	return github, home
}

func ensure(t *testing.T, answer string) (*Tools, string, error) {
	t.Helper()
	var out bytes.Buffer
	tools, err := Ensure(context.Background(), strings.NewReader(answer), &out)
	return tools, out.String(), err
}

func install(t *testing.T) *Tools {
	t.Helper()
	tools, _, err := ensure(t, "y\n")
	if err != nil {
		t.Fatal(err)
	}
	return tools
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if !exists(path) {
		t.Errorf("%s is missing", path)
	}
}

func TestFirstRunInstallsEverything(t *testing.T) {
	_, home := startGitHub(t)
	tools, out, err := ensure(t, "y\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"yt-dlp", "mpv", "Deno", "126 MB", home, "Setup finished"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	mustExist(t, tools.Mpv)
	mustExist(t, tools.YtDlp())
	mustExist(t, filepath.Join(filepath.Dir(tools.YtDlp()), "_internal", "base.dll"))
	if !strings.HasPrefix(tools.JSRuntime, "deno:") {
		t.Errorf("got %s", tools.JSRuntime)
	}
	mustExist(t, strings.TrimPrefix(tools.JSRuntime, "deno:"))
}

func TestOnlyTheNamedFileIsTakenFromAPinnedArchive(t *testing.T) {
	startGitHub(t)
	tools := install(t)
	entries, err := os.ReadDir(filepath.Dir(tools.Mpv))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "mpv.exe" {
		t.Fatalf("got %v", entries)
	}
}

func TestPressingEnterMeansYes(t *testing.T) {
	startGitHub(t)
	if _, _, err := ensure(t, "\n"); err != nil {
		t.Fatal(err)
	}
}

func TestDecliningInstallsNothing(t *testing.T) {
	github, home := startGitHub(t)
	for _, answer := range []string{"n\n", "no\n", "maybe\n", ""} {
		if _, _, err := ensure(t, answer); err == nil {
			t.Errorf("answer %q should stop setup", answer)
		}
	}
	if got := github.requests.Load(); got != 0 {
		t.Fatalf("made %d requests", got)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("left behind %v", entries)
	}
}

func TestSecondRunDownloadsNothingAndAsksNothing(t *testing.T) {
	github, _ := startGitHub(t)
	install(t)
	github.requests.Store(0)
	_, out, err := ensure(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Fatalf("printed %q", out)
	}
	if got := github.requests.Load(); got != 0 {
		t.Fatalf("made %d requests", got)
	}
}

func TestOnlyTheMissingToolIsDownloaded(t *testing.T) {
	_, home := startGitHub(t)
	install(t)
	if err := os.RemoveAll(filepath.Join(home, "deno")); err != nil {
		t.Fatal(err)
	}
	_, out, err := ensure(t, "y\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "41 MB") || strings.Contains(out, "mpv") {
		t.Fatalf("got:\n%s", out)
	}
}

func TestATamperedDownloadIsRefused(t *testing.T) {
	github, home := startGitHub(t)
	github.files[testMpvPath] = makeZip(t, map[string]string{"mpv.exe": "something else"})
	_, _, err := ensure(t, "y\n")
	if err == nil || !strings.Contains(err.Error(), "security check") {
		t.Fatalf("got %v", err)
	}
	if exists(mpvTool.exePath(home)) {
		t.Fatal("the bad file was installed")
	}
	if leftovers, _ := filepath.Glob(filepath.Join(home, tempPrefix+"*")); len(leftovers) != 0 {
		t.Fatalf("left behind %v", leftovers)
	}
}

func TestATamperedYtDlpIsRefused(t *testing.T) {
	github, home := startGitHub(t)
	github.files[releases+"2026.10.26/"+ytDlpArchive] = makeZip(t, map[string]string{"yt-dlp.exe": "evil"})
	if _, _, err := ensure(t, "y\n"); err == nil {
		t.Fatal("expected an error")
	}
	if installedYtDlp(home) != "" {
		t.Fatal("the bad file was installed")
	}
}

func TestAnArchiveCannotWriteOutsideItsFolder(t *testing.T) {
	github, home := startGitHub(t)
	data := makeZip(t, map[string]string{"../../evil.exe": "evil", "yt-dlp.exe": "ok"})
	github.files[releases+"2026.10.26/"+ytDlpArchive] = data
	github.setSums("2026.10.26", hashOf(data)+"  "+ytDlpArchive+"\n")
	_, _, err := ensure(t, "y\n")
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("got %v", err)
	}
	found := false
	filepath.Walk(filepath.Dir(home), func(path string, _ os.FileInfo, _ error) error {
		found = found || filepath.Base(path) == "evil.exe"
		return nil
	})
	if found {
		t.Fatal("evil.exe was written")
	}
}

func TestAnArchiveMissingItsProgramIsAnError(t *testing.T) {
	github, _ := startGitHub(t)
	data := makeZip(t, map[string]string{"readme.txt": "no program here"})
	github.files[testDenoPath] = data
	denoTool.sha256 = hashOf(data)
	_, _, err := ensure(t, "y\n")
	if err == nil || !strings.Contains(err.Error(), "deno.exe") {
		t.Fatalf("got %v", err)
	}
}

func TestANetworkFailureIsReportedAndCanBeRetried(t *testing.T) {
	github, _ := startGitHub(t)
	github.broken.Store(true)
	if _, _, err := ensure(t, "y\n"); err == nil {
		t.Fatal("expected an error")
	}
	github.broken.Store(false)
	tools := install(t)
	mustExist(t, tools.Mpv)
}

func TestLeftoversFromAnInterruptedInstallAreRemoved(t *testing.T) {
	_, home := startGitHub(t)
	leftover := filepath.Join(home, tempPrefix+"123")
	if err := os.MkdirAll(leftover, 0o755); err != nil {
		t.Fatal(err)
	}
	install(t)
	if exists(leftover) {
		t.Fatal("the leftover is still there")
	}
}

func TestTheVersionComesFromTheReleaseNotTheDownloadServer(t *testing.T) {
	startGitHub(t)
	tools := install(t)
	if got := filepath.Base(filepath.Dir(tools.YtDlp())); got != "2026.10.26" {
		t.Fatalf("installed as %q", got)
	}
}

func TestAStrangeVersionNameIsRefused(t *testing.T) {
	github, home := startGitHub(t)
	for _, version := range []string{"", "../../evil", "latest", "2026.10", "307260205"} {
		github.latest = version
		if _, _, err := ensure(t, "y\n"); err == nil {
			t.Errorf("version %q should be refused", version)
		}
	}
	if installedYtDlp(home) != "" {
		t.Fatal("something was installed")
	}
}

func TestAPointReleaseCountsAsNewerThanItsDay(t *testing.T) {
	github, home := startGitHub(t)
	tools := install(t)
	makeCheckDue(t, home)
	github.addYtDlp(t, "2026.10.26.1")
	github.latest = "2026.10.26.1"
	if version, err := tools.Update(context.Background()); err != nil || version != "2026.10.26.1" {
		t.Fatalf("got %q, %v", version, err)
	}
}

func TestAFolderWithoutAProgramDoesNotCountAsInstalled(t *testing.T) {
	_, home := startGitHub(t)
	if err := os.MkdirAll(filepath.Join(home, ytDlpDir, "2099.01.01"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := installedYtDlp(home); got != "" {
		t.Fatalf("got %s", got)
	}
}

func makeCheckDue(t *testing.T, home string) {
	t.Helper()
	old := time.Now().Add(-2 * updateInterval)
	if err := os.Chtimes(filepath.Join(home, ytDlpDir, lastCheck), old, old); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateDoesNothingIfCheckedRecently(t *testing.T) {
	github, _ := startGitHub(t)
	tools := install(t)
	github.requests.Store(0)
	version, err := tools.Update(context.Background())
	if err != nil || version != "" {
		t.Fatalf("got %q, %v", version, err)
	}
	if got := github.requests.Load(); got != 0 {
		t.Fatalf("made %d requests", got)
	}
}

func TestUpdateInstallsANewVersionAndRemovesTheOldOne(t *testing.T) {
	github, home := startGitHub(t)
	tools := install(t)
	old := tools.YtDlp()
	makeCheckDue(t, home)

	github.addYtDlp(t, "2026.11.03")
	github.latest = "2026.11.03"
	version, err := tools.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if version != "2026.11.03" {
		t.Fatalf("got %q", version)
	}
	if !strings.Contains(tools.YtDlp(), "2026.11.03") {
		t.Fatalf("still using %s", tools.YtDlp())
	}
	mustExist(t, tools.YtDlp())
	if exists(old) {
		t.Fatal("the old version is still there")
	}
}

func TestUpdateWithNoNewVersionWaitsUntilNextTime(t *testing.T) {
	github, home := startGitHub(t)
	tools := install(t)
	makeCheckDue(t, home)

	version, err := tools.Update(context.Background())
	if err != nil || version != "" {
		t.Fatalf("got %q, %v", version, err)
	}
	github.requests.Store(0)
	tools.Update(context.Background())
	if got := github.requests.Load(); got != 0 {
		t.Fatalf("checked again straight away: %d requests", got)
	}
}

func TestFailedUpdateKeepsTheOldVersionAndTriesAgain(t *testing.T) {
	github, home := startGitHub(t)
	tools := install(t)
	makeCheckDue(t, home)
	current := tools.YtDlp()

	github.broken.Store(true)
	if _, err := tools.Update(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	if tools.YtDlp() != current {
		t.Fatal("the version changed")
	}
	github.broken.Store(false)
	github.requests.Store(0)
	tools.Update(context.Background())
	if github.requests.Load() == 0 {
		t.Fatal("should try again on the next launch")
	}
}

func TestToolsFromPathNeverUpdate(t *testing.T) {
	tools := &Tools{ytDlpPath: "yt-dlp"}
	version, err := tools.Update(context.Background())
	if err != nil || version != "" {
		t.Fatalf("got %q, %v", version, err)
	}
	if tools.YtDlp() != "yt-dlp" {
		t.Fatalf("got %s", tools.YtDlp())
	}
}

func TestRealPinsLookSane(t *testing.T) {
	for _, tool := range []pinnedTool{mpvTool, denoTool} {
		if len(tool.sha256) != 64 {
			t.Errorf("%s: bad hash %q", tool.exe, tool.sha256)
		}
		if !strings.Contains(tool.path, tool.version) {
			t.Errorf("%s: path %s doesn't match version %s", tool.exe, tool.path, tool.version)
		}
	}
	if !strings.Contains(mpvTool.path, "mingw32") {
		t.Error("mpv must be the MinGW build: the MSVC build needs the Visual C++ runtime")
	}
}

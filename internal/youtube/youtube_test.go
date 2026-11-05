package youtube

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// The test binary doubles as a stand-in for yt-dlp: when this variable is set it
// acts out the requested behavior instead of running the tests.
const fakeMode = "YTUNE_FAKE_YTDLP"
const fakeLog = "YTUNE_FAKE_LOG"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeMode); mode != "" {
		os.Exit(fakeYtDlp(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeYtDlp(mode string, args []string) int {
	if log := os.Getenv(fakeLog); log != "" {
		file, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintln(file, strings.Join(args, "\x1f"))
			file.Close()
		}
	}
	switch mode {
	case "search":
		fmt.Print(`{"_type":"playlist","entries":[
			{"id":"a","title":"Song A","channel":"Artist","duration":61.7},
			null,
			{"title":"[Deleted video]"},
			{"id":"b","uploader":"Uploader"}]}`)
	case "single":
		fmt.Print(`{"id":"x","title":"One song","channel":"Artist","duration":200}`)
	case "download":
		time.Sleep(50 * time.Millisecond)
		template := args[slices.Index(args, "--output")+1]
		path := strings.ReplaceAll(strings.ReplaceAll(template, "%(ext)s", "webm"), "%%", "%")
		os.WriteFile(path, []byte("audio"), 0o644)
	case "noaudio":
	case "fail":
		fmt.Fprintln(os.Stderr, "WARNING: something minor")
		fmt.Fprintln(os.Stderr, "ERROR: [youtube] abc: Video unavailable")
		return 1
	case "garbled":
		fmt.Fprintln(os.Stderr, "Traceback: it broke")
		return 1
	case "silent":
		return 1
	case "badjson":
		fmt.Print("not json")
	case "hang":
		time.Sleep(time.Minute)
	}
	return 0
}

func newClient(t *testing.T, mode string) (*Client, string) {
	t.Helper()
	t.Setenv(fakeMode, mode)
	log := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv(fakeLog, log)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return New(context.Background(), func() string { return self }, "deno:C:/deno.exe", t.TempDir()), log
}

func calls(t *testing.T, log string) [][]string {
	t.Helper()
	data, err := os.ReadFile(log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var result [][]string
	for line := range strings.Lines(string(data)) {
		result = append(result, strings.Split(strings.TrimRight(line, "\r\n"), "\x1f"))
	}
	return result
}

func TestSearchReturnsTracksAndSkipsDeletedEntries(t *testing.T) {
	client, _ := newClient(t, "search")
	tracks, err := client.Search("tum hi ho")
	if err != nil {
		t.Fatal(err)
	}
	want := []Track{
		{ID: "a", Title: "Song A", Channel: "Artist", Duration: 61},
		{ID: "b", Title: "b", Channel: "Uploader"},
	}
	if !slices.Equal(tracks, want) {
		t.Fatalf("got %+v", tracks)
	}
}

func TestEveryRunNamesTheRuntimeAndIgnoresUserConfig(t *testing.T) {
	client, log := newClient(t, "search")
	if _, err := client.Search("anything"); err != nil {
		t.Fatal(err)
	}
	args := calls(t, log)[0]
	for _, want := range []string{"--ignore-config", "--no-playlist", "--js-runtimes", "deno:C:/deno.exe"} {
		if !slices.Contains(args, want) {
			t.Errorf("missing %s in %v", want, args)
		}
	}
}

func TestSearchTextCannotBeReadAsAnOption(t *testing.T) {
	client, log := newClient(t, "search")
	if _, err := client.Search("--exec evil"); err != nil {
		t.Fatal(err)
	}
	args := calls(t, log)[0]
	if args[len(args)-1] != "ytsearch20:--exec evil" || args[len(args)-2] != "--" {
		t.Fatalf("query should come after -- with its prefix: %v", args)
	}
}

func TestResolveHandlesASingleVideo(t *testing.T) {
	client, _ := newClient(t, "single")
	tracks, err := client.Resolve("https://youtu.be/x")
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 || tracks[0].ID != "x" {
		t.Fatalf("got %+v", tracks)
	}
}

func TestFetchDownloadsOnceThenReusesTheFile(t *testing.T) {
	client, log := newClient(t, "download")
	track := Track{ID: "abc", Title: "T"}
	first, err := client.Fetch(track)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(first) != "abc.webm" {
		t.Fatalf("got %s", first)
	}
	second, err := client.Fetch(track)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("%s != %s", first, second)
	}
	if got := len(calls(t, log)); got != 1 {
		t.Fatalf("yt-dlp ran %d times", got)
	}
}

func TestFetchingTheSameTrackAtOnceDownloadsItOnce(t *testing.T) {
	client, log := newClient(t, "download")
	track := Track{ID: "abc", Title: "T"}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if _, err := client.Fetch(track); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if got := len(calls(t, log)); got != 1 {
		t.Fatalf("yt-dlp ran %d times", got)
	}
}

func TestPercentInTheFolderNameIsEscaped(t *testing.T) {
	client, _ := newClient(t, "download")
	client.cacheDir = filepath.Join(t.TempDir(), "100%(id)s")
	if err := os.MkdirAll(client.cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path, err := client.Fetch(Track{ID: "abc", Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != client.cacheDir {
		t.Fatalf("got %s", path)
	}
}

func TestFetchReportsWhenNothingWasDownloaded(t *testing.T) {
	client, _ := newClient(t, "noaudio")
	_, err := client.Fetch(Track{ID: "abc", Title: "My song"})
	if err == nil || !strings.Contains(err.Error(), "My song") {
		t.Fatalf("got %v", err)
	}
}

func TestInterruptedDownloadsAreNotTreatedAsCached(t *testing.T) {
	client, _ := newClient(t, "noaudio")
	for _, name := range []string{"abc.webm.part", "abc.part", "abc.temp.webm", "abc.ytdl", "abcd.webm"} {
		if err := os.WriteFile(filepath.Join(client.cacheDir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if path := client.cached(Track{ID: "abc"}); path != "" {
		t.Fatalf("treated %s as finished", path)
	}
}

func TestErrorsShowYtDlpsOwnMessage(t *testing.T) {
	client, _ := newClient(t, "fail")
	_, err := client.Search("anything")
	if err == nil || err.Error() != "[youtube] abc: Video unavailable" {
		t.Fatalf("got %v", err)
	}
}

func TestErrorsWithoutAnErrorLineStillSaySomething(t *testing.T) {
	client, _ := newClient(t, "garbled")
	if _, err := client.Search("x"); err == nil || err.Error() != "Traceback: it broke" {
		t.Fatalf("got %v", err)
	}
	client, _ = newClient(t, "silent")
	if _, err := client.Search("x"); err == nil || !strings.Contains(err.Error(), "exit status") {
		t.Fatalf("got %v", err)
	}
}

func TestUnreadableOutputIsAnError(t *testing.T) {
	client, _ := newClient(t, "badjson")
	if _, err := client.Search("x"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestCancellingStopsARunningSearch(t *testing.T) {
	client, _ := newClient(t, "hang")
	ctx, cancel := context.WithCancel(context.Background())
	client.ctx = ctx
	done := make(chan error, 1)
	go func() {
		_, err := client.Search("x")
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the search kept running")
	}
}

func TestTheVersionOfYtDlpIsCheckedOnEveryRun(t *testing.T) {
	client, _ := newClient(t, "search")
	self, _ := os.Executable()
	used := 0
	client.ytDlp = func() string {
		used++
		return self
	}
	client.Search("x")
	client.Search("x")
	if used != 2 {
		t.Fatalf("looked up %d times", used)
	}
}

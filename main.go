// ytune is a full-screen terminal music player for YouTube.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jithin-jz/ytune/internal/player"
	"github.com/jithin-jz/ytune/internal/proc"
	"github.com/jithin-jz/ytune/internal/setup"
	"github.com/jithin-jz/ytune/internal/ui"
	"github.com/jithin-jz/ytune/internal/youtube"
)

const (
	version     = "0.2.0"
	cachePrefix = "ytune-"
	// A session's audio is normally deleted on quit. Anything older than this
	// was left behind by a crash or a forced shutdown.
	staleCacheAge = 24 * time.Hour
)

func main() {
	os.Exit(run())
}

func run() int {
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "A full-screen terminal music player for YouTube.")
		fmt.Fprintln(os.Stderr, "\nUsage:\n  ytune [search words or a YouTube link]")
		fmt.Fprintln(os.Stderr, "\nOptions:")
		flag.PrintDefaults()
	}
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("ytune", version)
		return 0
	}

	if err := play(strings.Join(flag.Args(), " ")); err != nil {
		fmt.Fprintln(os.Stderr, "ytune:", err)
		proc.WaitIfOwnConsole()
		return 1
	}
	return 0
}

func play(startup string) error {
	proc.KillChildrenOnExit()
	removeStaleCaches()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	setup.UserAgent = "ytune/" + version
	tools, err := setup.Ensure(ctx, os.Stdin, os.Stdout)
	if err != nil {
		return err
	}

	cacheDir, err := os.MkdirTemp("", cachePrefix)
	if err != nil {
		return fmt.Errorf("could not create a folder for audio: %w", err)
	}
	defer removeCache(cacheDir)

	mpv, err := player.Start(tools.Mpv)
	if err != nil {
		return err
	}
	defer mpv.Close()

	source := youtube.New(ctx, tools.YtDlp, tools.JSRuntime, cacheDir)
	update := func() (string, error) { return tools.Update(ctx) }

	model := ui.New(mpv, source, startup, update)
	_, err = tea.NewProgram(model, tea.WithFilter(ui.Filter)).Run()
	cancel() // Stops any download still running, so the cache can be deleted.

	// Closing the window or pressing Ctrl+C is an ordinary way to leave.
	if err != nil && !errors.Is(err, tea.ErrInterrupted) && !errors.Is(err, tea.ErrProgramKilled) {
		return err
	}
	return model.Err()
}

func removeCache(dir string) {
	// A download that was just stopped can hold its file open for a moment.
	for range 20 {
		if os.RemoveAll(dir) == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func removeStaleCaches() {
	dirs, _ := filepath.Glob(filepath.Join(os.TempDir(), cachePrefix+"*"))
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err == nil && time.Since(info.ModTime()) > staleCacheAge {
			os.RemoveAll(dir)
		}
	}
}

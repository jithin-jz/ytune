// Package youtube finds tracks and downloads their audio through yt-dlp.
package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jithin-jz/ytune/internal/proc"
)

const searchLimit = 20

// Track is one playable song.
type Track struct {
	ID       string
	Title    string
	Channel  string
	Duration int // Seconds, or 0 if unknown.
}

// URL returns the track's YouTube address.
func (t Track) URL() string {
	return "https://www.youtube.com/watch?v=" + t.ID
}

// Client runs yt-dlp and keeps downloaded audio in a folder.
type Client struct {
	ctx       context.Context
	ytDlp     func() string
	jsRuntime string
	cacheDir  string

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// New returns a Client. ytDlp is called before each run because the installed
// version can change while ytune is open. jsRuntime is in the form yt-dlp's
// --js-runtimes option expects. Cancelling ctx stops anything still running.
func New(ctx context.Context, ytDlp func() string, jsRuntime, cacheDir string) *Client {
	return &Client{
		ctx:       ctx,
		ytDlp:     ytDlp,
		jsRuntime: jsRuntime,
		cacheDir:  cacheDir,
		locks:     map[string]*sync.Mutex{},
	}
}

// Search returns the top YouTube results for query.
func (c *Client) Search(query string) ([]Track, error) {
	return c.list(fmt.Sprintf("ytsearch%d:%s", searchLimit, query))
}

// Resolve returns the tracks behind a video or playlist link.
func (c *Client) Resolve(url string) ([]Track, error) {
	return c.list(url)
}

func (c *Client) list(target string) ([]Track, error) {
	output, err := c.run("--flat-playlist", "--dump-single-json", "--", target)
	if err != nil {
		return nil, err
	}
	return parseTracks(output)
}

// Fetch returns a local audio file for t, downloading it if needed. Playback
// and prefetching can ask for the same track at once, so each track has a lock.
func (c *Client) Fetch(t Track) (string, error) {
	lock := c.lockFor(t.ID)
	lock.Lock()
	defer lock.Unlock()

	if path := c.cached(t); path != "" {
		return path, nil
	}
	// A % in the folder name would otherwise be read as part of the template.
	template := strings.ReplaceAll(filepath.Join(c.cacheDir, t.ID), "%", "%%") + ".%(ext)s"
	if _, err := c.run("--format", "bestaudio/best", "--quiet", "--no-progress", "--output", template, "--", t.URL()); err != nil {
		return "", err
	}
	if path := c.cached(t); path != "" {
		return path, nil
	}
	return "", fmt.Errorf("no audio was downloaded for %s", t.Title)
}

func (c *Client) lockFor(id string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	lock, ok := c.locks[id]
	if !ok {
		lock = &sync.Mutex{}
		c.locks[id] = lock
	}
	return lock
}

// cached finds a finished download. Partial files carry an extra suffix such as
// abc.webm.part, so a complete file is exactly the ID and one extension.
func (c *Client) cached(t Track) string {
	entries, err := os.ReadDir(c.cacheDir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		extension, found := strings.CutPrefix(entry.Name(), t.ID+".")
		if !found || strings.Contains(extension, ".") {
			continue
		}
		switch extension {
		case "part", "ytdl", "temp":
			continue
		}
		return filepath.Join(c.cacheDir, entry.Name())
	}
	return ""
}

func (c *Client) run(args ...string) ([]byte, error) {
	// Config files are ignored so that a user's own yt-dlp settings can't
	// change what gets downloaded or where it goes.
	common := []string{"--ignore-config", "--no-warnings", "--no-playlist", "--js-runtimes", c.jsRuntime}
	cmd := exec.CommandContext(c.ctx, c.ytDlp(), append(common, args...)...)
	proc.Hide(cmd)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, errors.New(errorMessage(stderr.String(), err))
	}
	return output, nil
}

// errorMessage picks the line worth showing out of yt-dlp's error output.
func errorMessage(stderr string, err error) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if message, found := strings.CutPrefix(strings.TrimSpace(lines[i]), "ERROR: "); found {
			return message
		}
	}
	if lines[0] != "" {
		return strings.TrimSpace(lines[0])
	}
	return err.Error()
}

type entry struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Channel  string    `json:"channel"`
	Uploader string    `json:"uploader"`
	Duration float64   `json:"duration"`
	Entries  *[]*entry `json:"entries"`
}

func (e entry) track() Track {
	track := Track{ID: e.ID, Title: e.Title, Channel: e.Channel, Duration: int(e.Duration)}
	if track.Title == "" {
		track.Title = e.ID
	}
	if track.Channel == "" {
		track.Channel = e.Uploader
	}
	return track
}

func parseTracks(output []byte) ([]Track, error) {
	var info entry
	if err := json.Unmarshal(output, &info); err != nil {
		return nil, fmt.Errorf("could not read yt-dlp's answer: %w", err)
	}
	if info.Entries == nil {
		return []Track{info.track()}, nil
	}
	tracks := make([]Track, 0, len(*info.Entries))
	for _, item := range *info.Entries {
		// Deleted and private videos come back as empty placeholders.
		if item != nil && item.ID != "" {
			tracks = append(tracks, item.track())
		}
	}
	return tracks, nil
}

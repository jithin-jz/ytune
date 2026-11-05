// Package player drives a headless mpv process over its JSON IPC channel.
package player

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

const (
	connectTimeout = 5 * time.Second
	replyTimeout   = 2 * time.Second
	writeTimeout   = 2 * time.Second
	quitTimeout    = 3 * time.Second
)

// ErrClosed means mpv has stopped running.
var ErrClosed = errors.New("mpv stopped")

// Status is a snapshot of playback. Position and Duration are -1 when unknown.
type Status struct {
	Position float64
	Duration float64
	Paused   bool
	Volume   int
	Finished bool // A track played to its end or failed since the last Status.
}

type message struct {
	RequestID int    `json:"request_id"`
	Data      any    `json:"data"`
	Event     string `json:"event"`
	Reason    string `json:"reason"`
}

// Mpv is a running mpv process.
type Mpv struct {
	cmd    *exec.Cmd
	conn   net.Conn
	closed chan struct{} // Closed once the connection is lost.
	exited chan struct{} // Closed once the process has ended.

	writeMu sync.Mutex

	mu      sync.Mutex
	nextID  int
	pending map[int]chan message

	finished atomic.Bool
}

// Start launches mpv without a window and connects to it.
func Start(path string, extraArgs ...string) (*Mpv, error) {
	address := ipcAddress(fmt.Sprintf("ytune-%d-%d", os.Getpid(), time.Now().UnixNano()))
	args := append([]string{
		"--idle=yes",
		"--no-video",
		"--no-terminal",
		"--input-ipc-server=" + address,
	}, extraArgs...)

	cmd := exec.Command(path, args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("could not start mpv: %w", err)
	}
	exited := make(chan struct{})
	go func() {
		cmd.Wait()
		close(exited)
	}()

	conn, err := connect(address, exited)
	if err != nil {
		cmd.Process.Kill()
		return nil, err
	}
	p := &Mpv{
		cmd:     cmd,
		conn:    conn,
		closed:  make(chan struct{}),
		exited:  exited,
		pending: map[int]chan message{},
	}
	go p.readLoop()
	return p, nil
}

func connect(address string, exited <-chan struct{}) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	for {
		conn, err := dial(ctx, address)
		if err == nil {
			return conn, nil
		}
		select {
		case <-exited:
			return nil, errors.New("mpv closed as soon as it started")
		case <-ctx.Done():
			return nil, fmt.Errorf("could not connect to mpv: %w", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (p *Mpv) readLoop() {
	defer close(p.closed)
	scanner := bufio.NewScanner(p.conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var m message
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			continue
		}
		// A replaced or stopped file ends with reason "stop", which must not
		// count as the track finishing on its own.
		if m.Event == "end-file" && (m.Reason == "eof" || m.Reason == "error") {
			p.finished.Store(true)
		}
		p.mu.Lock()
		reply, ok := p.pending[m.RequestID]
		delete(p.pending, m.RequestID)
		p.mu.Unlock()
		if ok {
			reply <- m
		}
	}
}

func (p *Mpv) write(payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	p.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, err = p.conn.Write(append(data, '\n'))
	return err
}

// send fires a command without waiting for the reply. Commands reach mpv in the
// order they were sent, which matters for a stop followed by a load. A lost
// connection is picked up by the next Status call.
func (p *Mpv) send(args ...any) {
	p.write(map[string]any{"command": args})
}

func (p *Mpv) request(args ...any) (message, error) {
	reply := make(chan message, 1)
	p.mu.Lock()
	p.nextID++
	id := p.nextID
	p.pending[id] = reply
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
	}()

	if err := p.write(map[string]any{"command": args, "request_id": id}); err != nil {
		return message{}, ErrClosed
	}
	select {
	case m := <-reply:
		return m, nil
	case <-p.closed:
		return message{}, ErrClosed
	case <-time.After(replyTimeout):
		return message{}, errors.New("mpv did not answer")
	}
}

// Load starts playing a file, replacing whatever is playing.
func (p *Mpv) Load(path string) {
	p.finished.Store(false)
	p.send("loadfile", path, "replace")
	p.send("set_property", "pause", false)
}

// Stop ends playback.
func (p *Mpv) Stop() {
	p.send("stop")
	p.finished.Store(false)
}

// TogglePause pauses or resumes.
func (p *Mpv) TogglePause() {
	p.send("cycle", "pause")
}

// Seek jumps by seconds, forwards or backwards.
func (p *Mpv) Seek(seconds float64) {
	p.send("seek", seconds, "relative")
}

// ChangeVolume raises or lowers the volume.
func (p *Mpv) ChangeVolume(delta int) {
	p.send("add", "volume", delta)
}

// Status reads the current playback state.
func (p *Mpv) Status() (Status, error) {
	values := map[string]any{}
	for _, name := range []string{"time-pos", "duration", "pause", "volume"} {
		reply, err := p.request("get_property", name)
		if err != nil {
			return Status{}, err
		}
		values[name] = reply.Data // Stays nil when nothing is loaded.
	}
	paused, _ := values["pause"].(bool)
	return Status{
		Position: number(values["time-pos"], -1),
		Duration: number(values["duration"], -1),
		Paused:   paused,
		Volume:   int(math.Round(number(values["volume"], 100))),
		Finished: p.finished.Swap(false),
	}, nil
}

func number(value any, fallback float64) float64 {
	if n, ok := value.(float64); ok {
		return n
	}
	return fallback
}

// Close stops mpv and waits for it to exit.
func (p *Mpv) Close() {
	p.send("quit")
	select {
	case <-p.exited:
	case <-time.After(quitTimeout):
		p.cmd.Process.Kill()
		<-p.exited
	}
	p.conn.Close()
}

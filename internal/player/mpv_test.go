package player

import (
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// These tests drive a real mpv, with sound turned off, because the IPC protocol
// and its timing are the point. They're skipped where mpv isn't installed.
func startMpv(t *testing.T) *Mpv {
	t.Helper()
	path, err := exec.LookPath("mpv.exe")
	if err != nil {
		path, err = exec.LookPath("mpv")
	}
	if err != nil {
		t.Skip("mpv is not installed")
	}
	p, err := Start(path, "--ao=null", "--no-config")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// writeSilence creates a silent WAV file lasting the given number of seconds.
func writeSilence(t *testing.T, seconds int) string {
	t.Helper()
	const rate = 8000
	size := uint32(rate * seconds)
	path := filepath.Join(t.TempDir(), "silence.wav")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	le := binary.LittleEndian
	file.WriteString("RIFF")
	binary.Write(file, le, 36+size)
	file.WriteString("WAVEfmt ")
	binary.Write(file, le, uint32(16))
	binary.Write(file, le, uint16(1)) // PCM
	binary.Write(file, le, uint16(1)) // Mono
	binary.Write(file, le, uint32(rate))
	binary.Write(file, le, uint32(rate))
	binary.Write(file, le, uint16(1))
	binary.Write(file, le, uint16(8))
	file.WriteString("data")
	binary.Write(file, le, size)
	silence := make([]byte, size)
	for i := range silence {
		silence[i] = 128
	}
	file.Write(silence)
	return path
}

func waitFor(t *testing.T, p *Mpv, what string, check func(Status) bool) Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, err := p.Status()
		if err != nil {
			t.Fatal(err)
		}
		if check(status) {
			return status
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
	return Status{}
}

func playing(s Status) bool { return s.Position > 0 }

func TestIdleStatusIsUnknown(t *testing.T) {
	p := startMpv(t)
	status, err := p.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Position != -1 || status.Duration != -1 {
		t.Fatalf("got %+v", status)
	}
	if status.Finished {
		t.Fatal("nothing has finished")
	}
}

func TestPlayPauseSeekAndVolume(t *testing.T) {
	p := startMpv(t)
	p.Load(writeSilence(t, 30))
	status := waitFor(t, p, "playback", playing)
	if status.Duration < 29 || status.Duration > 31 {
		t.Fatalf("got duration %v", status.Duration)
	}

	p.TogglePause()
	paused := waitFor(t, p, "pause", func(s Status) bool { return s.Paused })
	time.Sleep(200 * time.Millisecond)
	// The last frame can still land just after pausing, which moves the position
	// a few milliseconds. Anything still playing would have moved 200.
	if later, _ := p.Status(); later.Position-paused.Position > 0.1 {
		t.Fatalf("position moved while paused: %v -> %v", paused.Position, later.Position)
	}
	p.TogglePause()
	waitFor(t, p, "resume", func(s Status) bool { return !s.Paused })

	before, _ := p.Status()
	p.Seek(10)
	waitFor(t, p, "seek forward", func(s Status) bool { return s.Position >= before.Position+9 })
	p.Seek(-5)
	waitFor(t, p, "seek back", func(s Status) bool { return s.Position < before.Position+9 })

	p.ChangeVolume(-5)
	waitFor(t, p, "volume", func(s Status) bool { return s.Volume == 95 })
}

func TestLoadingResumesAPausedPlayer(t *testing.T) {
	p := startMpv(t)
	path := writeSilence(t, 30)
	p.TogglePause()
	waitFor(t, p, "pause", func(s Status) bool { return s.Paused })
	p.Load(path)
	waitFor(t, p, "playback", func(s Status) bool { return s.Position > 0 && !s.Paused })
}

func TestATrackEndingIsReportedExactlyOnce(t *testing.T) {
	p := startMpv(t)
	p.Load(writeSilence(t, 1))
	waitFor(t, p, "end of track", func(s Status) bool { return s.Finished })
	time.Sleep(100 * time.Millisecond)
	if status, _ := p.Status(); status.Finished {
		t.Fatal("reported twice")
	}
}

func TestStoppingOrReplacingATrackDoesNotCountAsFinishing(t *testing.T) {
	p := startMpv(t)
	path := writeSilence(t, 30)

	p.Load(path)
	waitFor(t, p, "playback", playing)
	p.Load(path)
	waitFor(t, p, "playback", playing)
	p.Stop()
	time.Sleep(300 * time.Millisecond)
	if status, _ := p.Status(); status.Finished {
		t.Fatal("stopping counted as finishing")
	}
}

func TestAFileThatCannotPlayIsReportedSoTheQueueMovesOn(t *testing.T) {
	p := startMpv(t)
	p.Load(filepath.Join(t.TempDir(), "missing.webm"))
	waitFor(t, p, "failure", func(s Status) bool { return s.Finished })
}

func TestCommandsArriveInOrder(t *testing.T) {
	p := startMpv(t)
	path := writeSilence(t, 30)
	for range 50 {
		p.Stop()
		p.Load(path)
	}
	waitFor(t, p, "playback", playing)
}

func TestCloseEndsTheProcess(t *testing.T) {
	p := startMpv(t)
	p.Close()
	select {
	case <-p.exited:
	default:
		t.Fatal("mpv is still running")
	}
	if _, err := p.Status(); !errors.Is(err, ErrClosed) {
		t.Fatalf("got %v", err)
	}
}

func TestAKilledPlayerIsDetected(t *testing.T) {
	p := startMpv(t)
	p.cmd.Process.Kill()
	<-p.closed
	if _, err := p.Status(); !errors.Is(err, ErrClosed) {
		t.Fatalf("got %v", err)
	}
	p.Load("anything") // Must not panic or block.
}

func TestAMissingProgramIsAnError(t *testing.T) {
	if _, err := Start(filepath.Join(t.TempDir(), "no-such-mpv.exe")); err == nil {
		t.Fatal("expected an error")
	}
}

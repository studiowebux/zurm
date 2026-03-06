package recorder

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// Recorder pipes raw RGBA frames to ffmpeg and produces an MP4 file.
// Pattern: adapter — wraps ffmpeg subprocess behind a simple Start/Stop/AddFrame API.
type Recorder struct {
	mu       sync.Mutex
	active   bool
	cmd      *exec.Cmd
	stdin   io.WriteCloser
	outPath string
	start   time.Time
	width   int
	height  int
}

// New creates a Recorder for the given frame dimensions.
func New(width, height int) *Recorder {
	return &Recorder{width: width, height: height}
}

// Resize updates the frame dimensions. Only takes effect on the next Start().
func (r *Recorder) Resize(width, height int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.width = width
	r.height = height
}

// Start spawns an ffmpeg subprocess that reads raw RGBA from stdin
// and encodes to H.264 MP4. Returns an error if ffmpeg is not installed
// or the recorder is already active.
func (r *Recorder) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.active {
		return fmt.Errorf("recording already active")
	}

	outDir := recordingDir()
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return fmt.Errorf("create recording dir: %w", err)
	}

	ts := time.Now().Format("2006-01-02-15-04-05")
	r.outPath = filepath.Join(outDir, "zurm-"+ts+".mp4")

	// #nosec G204 — arguments are not user-controlled; dimensions come from
	// Ebitengine screen bounds and the output path is built from time.Now().
	// The crop filter trims to even dimensions required by x264/yuv420p.
	r.cmd = exec.Command("ffmpeg",
		"-y",
		"-f", "rawvideo",
		"-pix_fmt", "rgba",
		"-s", fmt.Sprintf("%dx%d", r.width, r.height),
		"-r", "30",
		"-i", "pipe:0",
		"-vf", "crop=trunc(iw/2)*2:trunc(ih/2)*2",
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-pix_fmt", "yuv420p",
		r.outPath,
	)

	// Discard ffmpeg stderr to avoid pipe buffer deadlock.
	r.cmd.Stderr = io.Discard

	var err error
	r.stdin, err = r.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	if err := r.cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	r.start = time.Now()
	r.active = true
	return nil
}

// Stop closes the ffmpeg stdin (signaling EOF), waits for the process to
// finish, and returns the output file path. This may block for several
// seconds while ffmpeg finalizes the MP4 container.
func (r *Recorder) Stop() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.active {
		return "", fmt.Errorf("not recording")
	}
	r.active = false

	if err := r.stdin.Close(); err != nil {
		return "", fmt.Errorf("close stdin: %w", err)
	}

	if err := r.cmd.Wait(); err != nil {
		return "", fmt.Errorf("ffmpeg: %w", err)
	}

	return r.outPath, nil
}

// Active reports whether a recording is in progress.
func (r *Recorder) Active() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

// OutputMode returns the recording format identifier.
func (r *Recorder) OutputMode() string {
	return "MP4"
}

// StartTime returns when the current recording started.
func (r *Recorder) StartTime() time.Time {
	return r.start
}

// OutputSize returns the current size of the output MP4 file on disk.
// Returns 0 if the file doesn't exist yet or can't be stat'd.
func (r *Recorder) OutputSize() int64 {
	r.mu.Lock()
	path := r.outPath
	r.mu.Unlock()
	if path == "" {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// AddFrame writes one raw RGBA frame to the ffmpeg pipe.
// Frames with incorrect size (e.g. after window resize) are silently dropped.
// Safe to call from any goroutine.
func (r *Recorder) AddFrame(raw []byte) {
	r.mu.Lock()
	if !r.active {
		r.mu.Unlock()
		return
	}
	expected := r.width * r.height * 4
	if len(raw) != expected {
		r.mu.Unlock()
		return
	}
	w := r.stdin
	r.mu.Unlock()

	_, _ = w.Write(raw)
}

// recordingDir returns the directory where recordings are saved.
// Uses the same preference order as screenshots.
func recordingDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "zurm-recordings"
	}
	for _, sub := range []string{"Movies", "Pictures", "Desktop"} {
		dir := filepath.Join(home, sub)
		if _, err := os.Stat(dir); err == nil {
			return filepath.Join(dir, "zurm-recordings")
		}
	}
	return filepath.Join(home, "zurm-recordings")
}

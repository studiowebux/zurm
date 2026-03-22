package recorder

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// Recorder pipes raw RGBA frames to ffmpeg and produces an MP4 file.
// Pattern: adapter — wraps ffmpeg subprocess behind a simple Start/Stop/AddFrame API.
// Frames are sent via a buffered channel and written by a dedicated goroutine,
// ensuring all in-flight frames are flushed before ffmpeg stdin is closed.
type Recorder struct {
	mu        sync.Mutex
	active    bool
	cmd       *exec.Cmd
	stderrBuf bytes.Buffer
	outPath   string
	start     time.Time
	width     int
	height    int

	// Channel-based frame pipeline. AddFrame sends to frames, the writer
	// goroutine drains it. Stop() closes the channel, the writer finishes
	// writing remaining frames, then signals via writerDone.
	frames     chan []byte
	writerDone chan struct{}

	// Frame timing — tracks when the last frame was sent so AddFrame can
	// insert duplicate frames to fill timing gaps and keep playback speed
	// aligned with wall-clock time.
	lastFrameTime time.Time
}

const (
	// FrameDuration is the target interval between captured frames (~30fps).
	FrameDuration = 33 * time.Millisecond
	frameBufferSz = 60 // 2 seconds of buffered frames
)

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
		"-r", "30",
		r.outPath,
	)

	// Capture stderr into a buffer for error reporting. bytes.Buffer grows
	// as needed so it won't block ffmpeg (unlike an OS pipe buffer).
	r.stderrBuf.Reset()
	r.cmd.Stderr = &r.stderrBuf

	stdin, err := r.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	if err := r.cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	r.frames = make(chan []byte, frameBufferSz)
	r.writerDone = make(chan struct{})
	r.start = time.Now()
	r.lastFrameTime = r.start
	r.active = true

	// Writer goroutine: drains frames channel → ffmpeg stdin.
	// Exits when the channel is closed and fully drained.
	go func() {
		defer close(r.writerDone)
		for frame := range r.frames {
			_, _ = stdin.Write(frame)
		}
		_ = stdin.Close()
	}()

	return nil
}

// Stop signals the writer to flush remaining frames, waits for ffmpeg to
// finalize the MP4 container, and returns the output file path.
func (r *Recorder) Stop() (string, error) {
	r.mu.Lock()
	if !r.active {
		r.mu.Unlock()
		return "", fmt.Errorf("not recording")
	}
	r.active = false
	ch := r.frames
	done := r.writerDone
	r.mu.Unlock()

	// Close the channel — the writer goroutine will drain any remaining
	// frames and then close stdin, signaling EOF to ffmpeg.
	close(ch)
	<-done // wait for writer to finish

	if err := r.cmd.Wait(); err != nil {
		stderr := r.stderrBuf.String()
		if stderr != "" {
			return "", fmt.Errorf("ffmpeg: %w\nstderr: %s", err, stderr)
		}
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

// AddFrame sends one raw RGBA frame to the writer goroutine.
// If the frame interval exceeds one frame duration, duplicate frames are
// inserted to keep playback speed aligned with wall-clock time.
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
	now := time.Now()
	elapsed := now.Sub(r.lastFrameTime)
	r.lastFrameTime = now

	// Calculate how many frames this interval represents.
	// If >1 frame duration has passed, insert duplicates to fill the gap.
	copies := int(elapsed / FrameDuration)
	if copies < 1 {
		copies = 1
	}
	if copies > 10 {
		copies = 10 // cap to avoid flooding after long pauses
	}

	// Send under the lock so Stop() cannot close r.frames while we're sending.
	// The non-blocking select ensures we never block while holding the lock.
	for i := 0; i < copies; i++ {
		select {
		case r.frames <- raw:
		default:
			// Channel full — drop frame rather than blocking Draw().
			r.mu.Unlock()
			return
		}
	}
	r.mu.Unlock()
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

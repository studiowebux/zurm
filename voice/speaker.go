package voice

import (
	"fmt"
	"os/exec"
	"strconv"
	"sync"
)

// Speaker wraps the macOS `say` CLI for text-to-speech.
type Speaker struct {
	mu  sync.Mutex
	cmd *exec.Cmd
}

// Speak reads text aloud using the macOS `say` command.
// If already speaking, stops the current speech first.
// voice is the voice name (empty = system default); rate is words per minute.
func (s *Speaker) Speak(text, voice string, rate int) error {
	s.Stop()

	s.mu.Lock()
	defer s.mu.Unlock()

	args := make([]string, 0, 4)
	if voice != "" {
		args = append(args, "-v", voice)
	}
	if rate > 0 {
		args = append(args, "-r", strconv.Itoa(rate))
	}
	args = append(args, text)

	cmd := exec.Command("say", args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("voice speak: %w", err)
	}
	s.cmd = cmd

	// Wait in background so we can detect when speech finishes.
	go func() {
		_ = cmd.Wait()
		s.mu.Lock()
		if s.cmd == cmd {
			s.cmd = nil
		}
		s.mu.Unlock()
	}()

	return nil
}

// Stop kills any active speech process.
func (s *Speaker) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		s.cmd = nil
	}
}

// Active returns true if speech is currently in progress.
func (s *Speaker) Active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmd != nil
}

//go:build !windows

package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
)

// promptSecret asks for a line on the terminal without showing what is typed, so that a secret is not left on the
// screen or in the scrollback. It uses stty, which every system with a terminal to type on has, because the way to do
// this in Go without it is a dependency this command line does not otherwise need.
func promptSecret(prompt string) (string, error) {
	state, err := stty("-g")
	if err != nil {
		return "", fmt.Errorf("could not hide what is typed (is standard input a terminal?): %w", err)
	}
	if _, err := stty("-echo"); err != nil {
		return "", fmt.Errorf("could not hide what is typed: %w", err)
	}
	// The terminal must never be left unable to show what is typed, however this ends.
	defer func() { _, _ = stty(strings.TrimSpace(state)) }()

	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupted)

	_, _ = fmt.Fprint(os.Stderr, prompt)
	type line struct {
		text string
		err  error
	}
	read := make(chan line, 1)
	go func() {
		text, err := bufio.NewReader(os.Stdin).ReadString('\n')
		read <- line{text, err}
	}()
	select {
	case got := <-read:
		_, _ = fmt.Fprintln(os.Stderr)
		if got.err != nil && !errors.Is(got.err, io.EOF) {
			return "", got.err
		}
		return strings.TrimRight(got.text, "\r\n"), nil
	case <-interrupted:
		_, _ = fmt.Fprintln(os.Stderr)
		return "", errors.New("cancelled")
	}
}

// stty runs stty on the terminal that is standard input and returns what it printed.
func stty(args ...string) (string, error) {
	command := exec.Command("stty", args...)
	command.Stdin = os.Stdin
	out, err := command.Output()
	return string(out), err
}

package installer

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var installLog = &installerLogger{}

type installerLogger struct {
	mu   sync.Mutex
	file *os.File
}

type commandOptions struct {
	dir        string
	env        []string
	envLog     []string
	stdin      io.Reader
	echoStdout bool
	echoStderr bool
}

func InitLogging() (string, string, error) {
	filename := fmt.Sprintf("geass-install-%s.log", time.Now().UTC().Format("20060102-150405"))

	candidates := make([]string, 0, 3)
	if explicit := os.Getenv("GEASS_INSTALL_LOG"); explicit != "" {
		candidates = append(candidates, explicit)
	} else {
		candidates = append(candidates, filename)
	}
	candidates = append(candidates, filepath.Join(os.TempDir(), filename))

	var (
		file       *os.File
		selected   string
		note       string
		openErrors []string
	)

	for i, candidate := range candidates {
		absPath, err := filepath.Abs(candidate)
		if err != nil {
			openErrors = append(openErrors, fmt.Sprintf("%s: resolve path: %v", candidate, err))
			continue
		}

		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			openErrors = append(openErrors, fmt.Sprintf("%s: create log directory: %v", absPath, err))
			continue
		}

		file, err = os.OpenFile(absPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			openErrors = append(openErrors, fmt.Sprintf("%s: open log file: %v", absPath, err))
			continue
		}

		selected = absPath
		if i > 0 {
			note = fmt.Sprintf("Primary log path was not writable, using fallback log file: %s", absPath)
		}
		break
	}

	if file == nil {
		return "", "", fmt.Errorf("%s", strings.Join(openErrors, "; "))
	}

	installLog.mu.Lock()
	if installLog.file != nil {
		_ = installLog.file.Close()
	}
	installLog.file = file
	installLog.mu.Unlock()

	Logf("installer", "Log file initialized at %s", selected)
	if note != "" {
		Logf("installer", "%s", note)
	}
	return selected, note, nil
}

func CloseLogging() error {
	installLog.mu.Lock()
	defer installLog.mu.Unlock()

	if installLog.file == nil {
		return nil
	}

	err := installLog.file.Close()
	installLog.file = nil
	return err
}

func Logf(step, format string, args ...any) {
	installLog.writeLine(step, "info", fmt.Sprintf(format, args...))
}

func logCommand(step string, cmd *exec.Cmd, opts commandOptions) {
	Logf(step, "Running command: %s", formatCommand(cmd.Path, cmd.Args[1:]...))
	if cmd.Dir != "" {
		Logf(step, "Working directory: %s", cmd.Dir)
	}
	if len(opts.envLog) > 0 {
		Logf(step, "Environment overrides: %s", strings.Join(opts.envLog, ", "))
	}
}

func runCommand(step, name string, args ...string) error {
	return runCommandWithOptions(step, commandOptions{
		echoStdout: true,
		echoStderr: true,
	}, name, args...)
}

func runCommandWithOptions(step string, opts commandOptions, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = opts.dir
	cmd.Env = opts.env
	cmd.Stdin = opts.stdin

	stdoutWriter := newStreamWriter(step, "stdout", nil)
	stderrWriter := newStreamWriter(step, "stderr", nil)
	if opts.echoStdout {
		stdoutWriter.echo = os.Stdout
	}
	if opts.echoStderr {
		stderrWriter.echo = os.Stderr
	}

	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter

	logCommand(step, cmd, opts)
	err := cmd.Run()
	stdoutWriter.Flush()
	stderrWriter.Flush()
	if err != nil {
		Logf(step, "Command failed: %v", err)
		return fmt.Errorf("%s failed: %w", formatCommand(name, args...), err)
	}

	Logf(step, "Command completed successfully")
	return nil
}

func outputCommand(step, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer

	stdoutWriter := newStreamWriter(step, "stdout", &stdoutBuf)
	stderrWriter := newStreamWriter(step, "stderr", &stderrBuf)

	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter

	logCommand(step, cmd, commandOptions{})
	err := cmd.Run()
	stdoutWriter.Flush()
	stderrWriter.Flush()
	if err != nil {
		Logf(step, "Command failed: %v", err)
		if stderr := strings.TrimSpace(stderrBuf.String()); stderr != "" {
			return stdoutBuf.Bytes(), fmt.Errorf("%s failed: %w: %s", formatCommand(name, args...), err, stderr)
		}
		return stdoutBuf.Bytes(), fmt.Errorf("%s failed: %w", formatCommand(name, args...), err)
	}

	Logf(step, "Command completed successfully")
	return stdoutBuf.Bytes(), nil
}

func combinedOutputCommand(step, name string, args ...string) ([]byte, error) {
	return combinedOutputCommandWithOptions(step, commandOptions{}, name, args...)
}

func combinedOutputCommandWithOptions(step string, opts commandOptions, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = opts.dir
	cmd.Env = opts.env
	cmd.Stdin = opts.stdin

	var combined bytes.Buffer
	streamWriter := newStreamWriter(step, "combined", &combined)
	cmd.Stdout = streamWriter
	cmd.Stderr = streamWriter

	logCommand(step, cmd, opts)
	err := cmd.Run()
	streamWriter.Flush()
	if err != nil {
		Logf(step, "Command failed: %v", err)
		if out := strings.TrimSpace(combined.String()); out != "" {
			return combined.Bytes(), fmt.Errorf("%s failed: %w: %s", formatCommand(name, args...), err, out)
		}
		return combined.Bytes(), fmt.Errorf("%s failed: %w", formatCommand(name, args...), err)
	}

	Logf(step, "Command completed successfully")
	return combined.Bytes(), nil
}

func formatCommand(name string, args ...string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuote(name))
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func shellQuote(value string) string {
	if value == "" {
		return `""`
	}

	safe := true
	for _, r := range value {
		if !(r == '.' || r == '/' || r == '_' || r == '-' || r == ':' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			safe = false
			break
		}
	}
	if safe {
		return value
	}
	return strconv.Quote(value)
}

func (l *installerLogger) writeLine(step, stream, line string) {
	line = strings.TrimRight(line, "\n")
	if line == "" {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file == nil {
		return
	}

	_, _ = fmt.Fprintf(l.file, "%s [%s] [%s] %s\n",
		time.Now().UTC().Format(time.RFC3339),
		step,
		stream,
		line,
	)
}

type streamWriter struct {
	step   string
	stream string
	echo   io.Writer
	buf    *bytes.Buffer
	line   bytes.Buffer
}

func newStreamWriter(step, stream string, buf *bytes.Buffer) *streamWriter {
	return &streamWriter{
		step:   step,
		stream: stream,
		buf:    buf,
	}
}

func (w *streamWriter) Write(p []byte) (int, error) {
	if w.echo != nil {
		if _, err := w.echo.Write(p); err != nil {
			return 0, err
		}
	}
	if w.buf != nil {
		if _, err := w.buf.Write(p); err != nil {
			return 0, err
		}
	}

	for _, b := range p {
		if b == '\n' {
			installLog.writeLine(w.step, w.stream, w.line.String())
			w.line.Reset()
			continue
		}
		_ = w.line.WriteByte(b)
	}

	return len(p), nil
}

func (w *streamWriter) Flush() {
	if w.line.Len() == 0 {
		return
	}
	installLog.writeLine(w.step, w.stream, w.line.String())
	w.line.Reset()
}

// Package netcheck is what the operator's own image does when the operator asks it to take part in a check that the
// cluster enforces network policies. A policy is only words until the network plugin acts on it, and a plugin that does
// not enforce them accepts the policy without complaint, so the only way to know is to try: one pod listens, a policy
// says nobody may reach it, and another pod tries.
//
// The operator's image is used because it is the one image a cluster that installed Geass is sure to have: it ships
// inside the installer, so the check needs no registry and no network access.
package netcheck

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

const (
	// Port is where a server listens.
	Port = 8080
	// DefaultResultPath is where Kubernetes reads a container's last words from, which the operator reads back
	// from the pod's status. It needs no access to the pod's logs.
	DefaultResultPath = "/dev/termination-log"
)

// Result is what a probe found out.
type Result struct {
	// Open is whether a server that no policy covers could be reached. It is the control: if it could not, nothing
	// can be said about the other, because the network itself was at fault.
	Open bool
	// Closed is whether a server that a policy closes to everyone could be reached anyway.
	Closed bool
}

// Enforced says what the result means: whether the network plugin enforces policies. It is only meaningful when Known
// is true.
func (r Result) Enforced() bool { return r.Open && !r.Closed }

// Known reports whether the result says anything about enforcement: it does not when even the control failed.
func (r Result) Known() bool { return r.Open }

// String is how a probe reports, as one line the operator parses back.
func (r Result) String() string {
	return fmt.Sprintf("open=%s closed=%s", reach(r.Open), reach(r.Closed))
}

func reach(reachable bool) string {
	if reachable {
		return "reachable"
	}
	return "blocked"
}

// Parse reads what String wrote.
func Parse(message string) (Result, error) {
	var result Result
	var seenOpen, seenClosed bool
	for field := range strings.FieldsSeq(message) {
		name, value, found := strings.Cut(field, "=")
		if !found || (value != "reachable" && value != "blocked") {
			continue
		}
		switch name {
		case "open":
			result.Open, seenOpen = value == "reachable", true
		case "closed":
			result.Closed, seenClosed = value == "reachable", true
		}
	}
	if !seenOpen || !seenClosed {
		return Result{}, fmt.Errorf("not a probe result: %q", message)
	}
	return result, nil
}

// Run is what `manager netcheck ...` does. It returns the process's exit status.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: netcheck serve --listen :8080 | netcheck probe --open host:port --closed host:port")
		return 2
	}
	switch args[0] {
	case "serve":
		flags := flag.NewFlagSet("serve", flag.ContinueOnError)
		flags.SetOutput(stderr)
		listen := flags.String("listen", fmt.Sprintf(":%d", Port), "the address to listen on")
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		if err := Serve(context.Background(), *listen); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "probe":
		flags := flag.NewFlagSet("probe", flag.ContinueOnError)
		flags.SetOutput(stderr)
		open := flags.String("open", "", "the server no policy covers, as host:port")
		closed := flags.String("closed", "", "the server a policy closes to everyone, as host:port")
		delay := flags.Duration("delay", 8*time.Second, "how long to wait before the first connection, for the network plugin to catch up with pods that are new")
		timeout := flags.Duration("timeout", 4*time.Second, "how long to wait for one connection")
		resultPath := flags.String("result", DefaultResultPath, "where to write the result for the operator to read")
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		if *open == "" || *closed == "" {
			_, _ = fmt.Fprintln(stderr, "probe needs --open and --closed")
			return 2
		}
		result := Probe(context.Background(), *open, *closed, *delay, *timeout)
		_, _ = fmt.Fprintln(stdout, result)
		if err := os.WriteFile(*resultPath, []byte(result.String()), 0o600); err != nil {
			_, _ = fmt.Fprintln(stderr, "could not write the result:", err)
		}
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "unknown netcheck command %q\n", args[0])
		return 2
	}
}

// Serve answers every connection until the context ends.
func Serve(ctx context.Context, address string) error {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", address)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		_, _ = conn.Write([]byte("geass\n"))
		_ = conn.Close()
	}
}

// Probe tries both servers. The one nothing closes is tried for as long as it takes to answer, up to a few
// attempts, because a pod that has only just started can be unreachable for a moment, whatever the policies say.
// The one a policy closes is tried once: a plugin that enforces policies refuses every attempt, and one that does
// not accepts the first, so more attempts only add the chance of a mistake.
func Probe(ctx context.Context, open, closed string, delay, timeout time.Duration) Result {
	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return Result{}
	}
	var result Result
	for range 5 {
		if reachable(ctx, open, timeout) {
			result.Open = true
			break
		}
		time.Sleep(time.Second)
	}
	if !result.Open {
		return result
	}
	result.Closed = reachable(ctx, closed, timeout)
	return result
}

func reachable(ctx context.Context, address string, timeout time.Duration) bool {
	conn, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", address)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

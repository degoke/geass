package netcheck

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func listen(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	go func() { _ = Serve(ctx, address) }()
	require.Eventually(t, func() bool {
		conn, err := net.Dial("tcp", address)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 5*time.Second, 20*time.Millisecond)
	return address
}

// closedPort is an address nothing listens on, which is what a policy that is enforced looks like from outside.
func closedPort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	return address
}

func TestAPolicyThatIsEnforcedLooksLikeAServerThatCannotBeReached(t *testing.T) {
	result := Probe(context.Background(), listen(t), closedPort(t), 0, time.Second)

	require.Equal(t, Result{Open: true, Closed: false}, result)
	require.True(t, result.Known())
	require.True(t, result.Enforced())
}

func TestAPolicyThatIsNotEnforcedLetsBothThrough(t *testing.T) {
	result := Probe(context.Background(), listen(t), listen(t), 0, time.Second)

	require.Equal(t, Result{Open: true, Closed: true}, result)
	require.True(t, result.Known())
	require.False(t, result.Enforced(), "the closed server answered, so what closed it is not doing anything")
}

func TestWhenEvenTheControlFailsNothingCanBeSaid(t *testing.T) {
	result := Probe(context.Background(), closedPort(t), listen(t), 0, 200*time.Millisecond)

	require.False(t, result.Known(), "a network that reached nothing proves nothing about a policy: it could be down")
	require.False(t, result.Closed, "and the closed server is not even tried then")
}

func TestAProbeThatIsCancelledSaysNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.False(t, Probe(ctx, listen(t), listen(t), time.Hour, time.Second).Known())
}

func TestAResultSurvivesTheTripThroughTheTerminationMessage(t *testing.T) {
	for _, want := range []Result{{Open: true}, {Open: true, Closed: true}, {}, {Closed: true}} {
		got, err := Parse(want.String())
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	got, err := Parse("some noise\nopen=reachable closed=blocked\n")
	require.NoError(t, err)
	require.Equal(t, Result{Open: true}, got, "what a probe prints is found among whatever else the container says")
}

func TestAMessageThatIsNotAResultIsRefused(t *testing.T) {
	for _, message := range []string{"", "open=reachable", "closed=blocked", "open=maybe closed=blocked", "OOMKilled"} {
		_, err := Parse(message)
		require.Error(t, err, message)
	}
}

func TestTheProbeCommandWritesItsResultWhereTheOperatorReadsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "termination-log")
	var stdout, stderr bytes.Buffer

	code := Run([]string{"probe", "--open", listen(t), "--closed", closedPort(t), "--delay", "0s", "--timeout", "1s", "--result", path}, &stdout, &stderr)

	require.Zero(t, code, stderr.String())
	require.Equal(t, "open=reachable closed=blocked", strings.TrimSpace(stdout.String()))
	written, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "open=reachable closed=blocked", string(written))
}

func TestTheCommandRefusesWhatItDoesNotUnderstand(t *testing.T) {
	var out bytes.Buffer
	require.Equal(t, 2, Run(nil, &out, &out))
	require.Equal(t, 2, Run([]string{"dance"}, &out, &out))
	require.Equal(t, 2, Run([]string{"probe"}, &out, &out), "a probe with nothing to probe is a mistake, not a pass")
}

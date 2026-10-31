package installer

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func noEnv(string) string { return "" }

func envOf(pairs map[string]string) func(string) string {
	return func(k string) string { return pairs[k] }
}

func TestNoArgumentsMeansDoWhatTheMachineNeeds(t *testing.T) {
	opts, err := ParseOptions(nil, noEnv)
	require.NoError(t, err)
	require.Empty(t, opts.Mode, "nothing is asked for, so nothing destructive is")
	require.False(t, opts.Yes)
}

func TestTheOptionsAndTheirEnvironmentVariables(t *testing.T) {
	opts, err := ParseOptions([]string{"--upgrade", "--force", "--skip-snapshot", "--k3s-version", "v1.35.1+k3s1", "--allow-untested-k3s"}, noEnv)
	require.NoError(t, err)
	require.Equal(t, ModeUpgrade, opts.Mode)
	require.True(t, opts.Upgrade.Force)
	require.True(t, opts.Upgrade.SkipSnapshot)
	require.True(t, opts.Upgrade.AllowUntestedK3s)
	require.Equal(t, "v1.35.1+k3s1", opts.K3sVersion)
	require.Equal(t, "v1.35.1+k3s1", opts.Upgrade.K3sVersion)

	opts, err = ParseOptions(nil, envOf(map[string]string{"GEASS_REINSTALL": "1", "GEASS_YES": "true", "GEASS_K3S_VERSION": "v1.34.2+k3s1"}))
	require.NoError(t, err)
	require.Equal(t, ModeReinstall, opts.Mode)
	require.True(t, opts.Yes)
	require.Equal(t, "v1.34.2+k3s1", opts.K3sVersion)

	opts, err = ParseOptions([]string{"--k3s-version", "v1.35.1+k3s1"}, envOf(map[string]string{"GEASS_K3S_VERSION": "v1.34.2+k3s1"}))
	require.NoError(t, err)
	require.Equal(t, "v1.35.1+k3s1", opts.K3sVersion, "the option wins over the environment")
}

func TestTheK3sVersionVariableDoesNotUpgradeAClusterThatExists(t *testing.T) {
	// Left set from the day of the install, it is for building a cluster. An update that reads it as --k3s-version would
	// restart the control plane's K3s, and then every worker's, on a run that asked for a Geass release only.
	opts, err := ParseOptions(nil, envOf(map[string]string{"GEASS_K3S_VERSION": "v1.35.1+k3s1"}))
	require.NoError(t, err)
	require.Equal(t, "v1.35.1+k3s1", opts.K3sVersion, "a new cluster is still built on it")
	require.Empty(t, opts.Upgrade.K3sVersion, "an update does not upgrade K3s because of it")
	require.Empty(t, NewUpgradePlan(opts.Upgrade).K3sTo)

	opts, err = ParseOptions([]string{"--upgrade", "--k3s-version", "v1.35.1+k3s1"}, envOf(map[string]string{"GEASS_K3S_VERSION": "v1.34.2+k3s1"}))
	require.NoError(t, err)
	require.Equal(t, "v1.35.1+k3s1", opts.Upgrade.K3sVersion, "the option upgrades it, to the version it names")

	_, err = ParseOptions(nil, envOf(map[string]string{"GEASS_K3S_VERSION": "stable"}))
	require.ErrorContains(t, err, "is not a K3s release", "the variable is still checked")
}

func TestUpgradeReadsAsAWordToo(t *testing.T) {
	opts, err := ParseOptions([]string{"upgrade", "--force"}, noEnv)
	require.NoError(t, err)
	require.Equal(t, ModeUpgrade, opts.Mode)
}

func TestReinstallAndUpgradeContradictEachOther(t *testing.T) {
	_, err := ParseOptions([]string{"--reinstall", "--upgrade"}, noEnv)
	require.ErrorContains(t, err, "contradict")
}

func TestAK3sVersionThatIsNotAReleaseIsRefusedBeforeAnythingRuns(t *testing.T) {
	for _, v := range []string{"stable", "latest", "v1.35", "v1.35.1", "v1.35.1+k3s1; rm -rf /"} {
		_, err := ParseOptions([]string{"--k3s-version", v}, noEnv)
		require.Error(t, err, v)
		require.Contains(t, err.Error(), "not a K3s release", v)
	}
}

func TestUnknownOptionsAndStrayWordsAreErrors(t *testing.T) {
	_, err := ParseOptions([]string{"--frobnicate"}, noEnv)
	require.Error(t, err)
	_, err = ParseOptions([]string{"cluster"}, noEnv)
	require.ErrorContains(t, err, "unexpected argument")
}

func TestHelpAndVersionAreOptions(t *testing.T) {
	opts, err := ParseOptions([]string{"--help"}, noEnv)
	require.NoError(t, err)
	require.True(t, opts.Help)
	opts, err = ParseOptions([]string{"--version"}, noEnv)
	require.NoError(t, err)
	require.True(t, opts.Version)
	require.Contains(t, Usage(), "--reinstall")
	require.Contains(t, Usage(), "DELETE", "the one option that deletes says so")
}

func TestWhichModeAMachineGets(t *testing.T) {
	boom := errors.New("connection refused")
	for _, tc := range []struct {
		name    string
		want    Mode
		here    Existing
		mode    Mode
		wantErr string
	}{
		{name: "a bare machine is installed", here: Existing{}, mode: ModeInstall},
		{name: "a machine with Geass is upgraded, not wiped", here: Existing{K3sPresent: true, GeassPresent: true}, mode: ModeUpgrade},
		{name: "upgrade asked for", want: ModeUpgrade, here: Existing{K3sPresent: true, GeassPresent: true}, mode: ModeUpgrade},
		{name: "reinstall asked for", want: ModeReinstall, here: Existing{K3sPresent: true, GeassPresent: true}, mode: ModeReinstall},
		{name: "reinstall of a machine with a broken K3s", want: ModeReinstall, here: Existing{K3sPresent: true, Unreachable: boom}, mode: ModeReinstall},
		{name: "reinstall of a bare machine is an install", want: ModeReinstall, here: Existing{}, mode: ModeInstall},
		{name: "upgrade of a bare machine", want: ModeUpgrade, here: Existing{}, wantErr: "nothing to upgrade"},
		{name: "K3s without Geass is not wiped for it", here: Existing{K3sPresent: true}, wantErr: "will not remove a K3s that it did not set up"},
		{name: "a worker is not a control plane", here: Existing{K3sPresent: true, Agent: true}, wantErr: "is a worker"},
		{name: "K3s that does not answer", here: Existing{K3sPresent: true, Unreachable: boom}, wantErr: "did not answer"},
		{name: "install over Geass", want: ModeInstall, here: Existing{K3sPresent: true, GeassPresent: true}, wantErr: "already has Geass installed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode, err := ChooseMode(tc.want, tc.here)
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
				if tc.here.K3sPresent {
					require.Contains(t, err.Error(), "--reinstall", "the way to what was wanted is in the message")
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.mode, mode)
		})
	}
}

func TestNothingButAnExplicitRequestEverReinstalls(t *testing.T) {
	// Every machine state, with nothing asked for: none of them is a reinstall.
	for _, here := range []Existing{
		{}, {K3sPresent: true}, {K3sPresent: true, GeassPresent: true}, {K3sPresent: true, Agent: true},
		{K3sPresent: true, Unreachable: errors.New("x")},
	} {
		mode, _ := ChooseMode("", here)
		require.NotEqual(t, ModeReinstall, mode, "%+v", here)
	}
}

// ---- the question before a reinstall ------------------------------------------------------------------------------------

type tty struct {
	io.Reader
	out bytes.Buffer
}

func (t *tty) Write(p []byte) (int, error) { return t.out.Write(p) }
func (t *tty) Close() error                { return nil }

func opener(answer string) (func() (io.ReadWriteCloser, error), *tty) {
	terminal := &tty{Reader: strings.NewReader(answer)}
	return func() (io.ReadWriteCloser, error) { return terminal, nil }, terminal
}

func TestAReinstallThatIsConfirmedOnTheTerminalGoesAhead(t *testing.T) {
	open, terminal := opener("reinstall\n")
	require.NoError(t, (&ConfirmReinstall{OpenTerminal: open}).Run())
	require.Contains(t, terminal.out.String(), "DELETES", "what is about to happen is said before it is asked")
	require.Contains(t, terminal.out.String(), reinstallSnapshotDir)
}

func TestTheQuestionDoesNotPromiseASnapshotThatWillNotBeTaken(t *testing.T) {
	open, terminal := opener("reinstall\n")
	require.NoError(t, (&ConfirmReinstall{OpenTerminal: open}).Run())
	require.Contains(t, terminal.out.String(), "try to keep", "a snapshot may not be possible, and the question says so")
	require.NotContains(t, terminal.out.String(), "is kept in")

	open, terminal = opener("reinstall\n")
	require.NoError(t, (&ConfirmReinstall{OpenTerminal: open, SkipSnapshot: true}).Run())
	require.Contains(t, terminal.out.String(), "No snapshot will be kept")
	require.NotContains(t, terminal.out.String(), reinstallSnapshotDir)
}

func TestAReinstallThatIsNotConfirmedDeletesNothing(t *testing.T) {
	for _, answer := range []string{"yes\n", "\n", "Reinstall please\n", ""} {
		open, _ := opener(answer)
		err := (&ConfirmReinstall{OpenTerminal: open}).Run()
		require.Error(t, err, "answer %q", answer)
		require.Contains(t, err.Error(), "nothing was deleted")
	}
}

func TestAReinstallWithNoTerminalNeedsYes(t *testing.T) {
	noTerminal := func() (io.ReadWriteCloser, error) { return nil, errors.New("no tty") }
	err := (&ConfirmReinstall{OpenTerminal: noTerminal}).Run()
	require.ErrorContains(t, err, "--yes")

	require.NoError(t, (&ConfirmReinstall{Yes: true, OpenTerminal: noTerminal}).Run(), "a script that said so in advance is not asked")
}

func TestTheSnapshotBeforeAReinstallGoesWhereTheUninstallCannotReachIt(t *testing.T) {
	host := &fakeHost{}
	useHost(t, host)
	var made string
	oldMkdir := mkdirAll
	mkdirAll = func(path string, _ os.FileMode) error { made = path; return nil }
	t.Cleanup(func() { mkdirAll = oldMkdir })

	step := ReinstallSnapshot(NewUpgradePlan(UpgradeOptions{}))
	require.NoError(t, step.Run())
	require.Equal(t, reinstallSnapshotDir, made)
	require.True(t, host.ran("etcd-snapshot save", "--dir "+reinstallSnapshotDir), "K3s's own snapshot directory is deleted with everything else: %v", host.commands())
}

func TestAReinstallGoesAheadWhenTheSnapshotCannotBeTaken(t *testing.T) {
	// A cluster that is thrown away because it is broken may be one whose etcd cannot be snapshotted.
	host := &fakeHost{answer: func(fakeCall) ([]byte, error) { return nil, errors.New("etcd is not running") }}
	useHost(t, host)
	oldMkdir := mkdirAll
	mkdirAll = func(string, os.FileMode) error { return nil }
	t.Cleanup(func() { mkdirAll = oldMkdir })

	require.NoError(t, ReinstallSnapshot(NewUpgradePlan(UpgradeOptions{})).Run())
}

func TestDashboardLinkIsACommandOfItsOwnOnAServer(t *testing.T) {
	opts, err := ParseOptions([]string{"dashboard-link"}, noEnv)
	require.NoError(t, err)
	require.Equal(t, ModeDashboardLink, opts.Mode)
	_, err = ParseOptions([]string{"dashboard-link", "--reinstall"}, noEnv)
	require.ErrorContains(t, err, "cannot be combined")

	mode, err := ChooseMode(ModeDashboardLink, Existing{K3sPresent: true, GeassPresent: true})
	require.NoError(t, err)
	require.Equal(t, ModeDashboardLink, mode)
	_, err = ChooseMode(ModeDashboardLink, Existing{})
	require.ErrorContains(t, err, "no K3s")
	_, err = ChooseMode(ModeDashboardLink, Existing{K3sPresent: true, Agent: true})
	require.ErrorContains(t, err, "worker")
}

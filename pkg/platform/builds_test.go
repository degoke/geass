package platform

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

const testDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestGitBuildLabelsCarryTheLabelBuildsAreFoundBy(t *testing.T) {
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "api"},
		Spec:       geassv1alpha1.GeassAppSpec{Project: "proj-1", Environment: geassv1alpha1.EnvironmentProduction},
	}

	labels := GitBuildLabels(app)

	require.Equal(t, "api", labels[LabelApp])
	require.Equal(t, "proj-1", labels[LabelProject])
	require.Equal(t, "production", labels[LabelEnvironment])
	require.Equal(t, ManagedByValue, labels[LabelManagedBy])
}

func TestResetBuildStatusForgetsEverythingARunRecorded(t *testing.T) {
	now := metav1.Now()
	status := geassv1alpha1.GeassBuildStatus{
		Phase: geassv1alpha1.GeassBuildSucceeded, ImageDigest: testDigest, SourceRevision: "abc", CIState: "success",
		CIChecks: []string{"test"}, LogRef: "object://logs/builds/b.log", FailureReason: "old", RetryCount: 2,
		StartedAt: &now, CompletedAt: &now, CancelRequested: true, Image: "registry/api:1", JobRef: "b-kaniko",
	}

	ResetBuildStatus(&status)

	require.Equal(t, geassv1alpha1.GeassBuildStatus{Phase: geassv1alpha1.GeassBuildPending, Image: "registry/api:1", JobRef: "b-kaniko"}, status,
		"only what names the build and its job stays: the digest, commit, checks and log of the last run go")
}

func TestNewestBuildIsTheLastCreated(t *testing.T) {
	at := func(name string, hour int) geassv1alpha1.GeassBuild {
		return geassv1alpha1.GeassBuild{ObjectMeta: metav1.ObjectMeta{Name: name, CreationTimestamp: metav1.NewTime(time.Date(2026, 1, 1, hour, 0, 0, 0, time.UTC))}}
	}

	require.Nil(t, NewestBuild(nil))
	require.Equal(t, "newest", NewestBuild([]geassv1alpha1.GeassBuild{at("oldest", 1), at("newest", 9), at("middle", 5)}).Name)
	require.Equal(t, "newest", NewestBuild([]geassv1alpha1.GeassBuild{at("newest", 9), at("oldest", 1)}).Name, "whatever the order of the list")
	require.Equal(t, "b", NewestBuild([]geassv1alpha1.GeassBuild{at("a", 3), at("b", 3)}).Name, "a tie goes to the later name, in either order")
	require.Equal(t, "b", NewestBuild([]geassv1alpha1.GeassBuild{at("b", 3), at("a", 3)}).Name)
}

func TestDigestFromKanikoLogReadsThePushedLine(t *testing.T) {
	// What Kaniko v1.23.2 prints, from DoPush: the digest follows the repository and is not a word of its own.
	log := `INFO[0001] Retrieving image manifest alpine:3.20
INFO[0003] Using cached layer sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
INFO[0010] Pushing image to registry.example.com/acme/api:main
INFO[0012] Pushed registry.example.com/acme/api@` + testDigest + `
`
	require.Equal(t, testDigest, DigestFromKanikoLog(log))
	require.Empty(t, DigestFromKanikoLog("INFO[0012] Skipping push to container registry due to --no-push flag"))
	require.Empty(t, DigestFromKanikoLog("sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"), "a bare digest is a layer, not the pushed image")
}

func TestImageReferenceWithDigest(t *testing.T) {
	for name, tt := range map[string]struct{ image, want string }{
		"tagged":                  {"registry.example.com/acme/api:main", "registry.example.com/acme/api@" + testDigest},
		"untagged":                {"ghcr.io/acme/api", "ghcr.io/acme/api@" + testDigest},
		"a port in the host":      {"localhost:5000/acme/api", "localhost:5000/acme/api@" + testDigest},
		"a port and a tag":        {"localhost:5000/acme/api:v1", "localhost:5000/acme/api@" + testDigest},
		"already has a digest":    {"ghcr.io/acme/api@sha256:aaaa", "ghcr.io/acme/api@" + testDigest},
		"docker hub short":        {"acme/api:1", "acme/api@" + testDigest},
		"tag and digest together": {"ghcr.io/acme/api:main@sha256:aaaa", "ghcr.io/acme/api@" + testDigest},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ImageReferenceWithDigest(tt.image, testDigest)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}

	_, err := ImageReferenceWithDigest("ghcr.io/acme/api", "sha256:abc")
	require.Error(t, err, "a short value is not a digest")
	_, err = ImageReferenceWithDigest("", testDigest)
	require.Error(t, err, "there is nothing to pull it from")
}

func TestBuildPaths(t *testing.T) {
	for name, tt := range map[string]struct{ root, dockerfile, wantContext, wantDockerfile string }{
		"defaults":                   {"", "", "/workspace", "/workspace/Dockerfile"},
		"dot is the root":            {".", "Dockerfile", "/workspace", "/workspace/Dockerfile"},
		"dockerfile in a subfolder":  {"", "deploy/Dockerfile", "/workspace", "/workspace/deploy/Dockerfile"},
		"root directory":             {"apps/api", "Dockerfile", "/workspace/apps/api", "/workspace/apps/api/Dockerfile"},
		"leading slash is the root":  {"/apps/api", "/Dockerfile", "/workspace/apps/api", "/workspace/apps/api/Dockerfile"},
		"dockerfile under the root":  {"apps/api", "docker/prod.Dockerfile", "/workspace/apps/api", "/workspace/apps/api/docker/prod.Dockerfile"},
		"the old full path":          {"apps/api", "apps/api/Dockerfile", "/workspace/apps/api", "/workspace/apps/api/Dockerfile"},
		"a prefix is not a folder":   {"apps/api", "apps/api-tools/Dockerfile", "/workspace/apps/api", "/workspace/apps/api/apps/api-tools/Dockerfile"},
		"windows separators":         {`apps\api`, `docker\Dockerfile`, "/workspace/apps/api", "/workspace/apps/api/docker/Dockerfile"},
		"redundant slashes and dots": {"./apps//api/", "./Dockerfile", "/workspace/apps/api", "/workspace/apps/api/Dockerfile"},
	} {
		t.Run(name, func(t *testing.T) {
			contextDir, dockerfile, err := BuildPaths("/workspace", tt.root, tt.dockerfile)
			require.NoError(t, err)
			require.Equal(t, tt.wantContext, contextDir)
			require.Equal(t, tt.wantDockerfile, dockerfile)
		})
	}

	for name, tt := range map[string]struct{ root, dockerfile string }{
		"root leaves the repository":       {"../other", "Dockerfile"},
		"dockerfile leaves the repository": {"", "../../etc/passwd"},
		"hidden in the middle":             {"apps/../../x", "Dockerfile"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := BuildPaths("/workspace", tt.root, tt.dockerfile)
			require.Error(t, err)
		})
	}
}

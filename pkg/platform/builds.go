package platform

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// GitBuildLabels are the labels on every GeassBuild of a service. LabelApp is the one a service's builds are
// found by: the push webhook, the build actions, the status read and the clean-up of old builds all select on
// it, so a build made without it cannot be rebuilt, reported or pruned. The operator and the dashboard both
// label the builds they make with this function, and tests label theirs with it, so a change to the label
// changes it everywhere at once.
func GitBuildLabels(app *geassv1alpha1.GeassApp) map[string]string {
	return map[string]string{
		LabelManagedBy:   ManagedByValue,
		LabelApp:         app.Name,
		LabelProject:     app.Spec.Project,
		LabelEnvironment: string(app.Spec.Environment),
	}
}

// ResetBuildStatus puts a build back to the start so the controller runs it again. Everything one run
// recorded is cleared with it: a build that kept the digest of its last run would report that image as the
// result of a run that has not happened yet, and one that kept its commit would build the old commit again.
// The controller resolves the commit and the check results afresh and writes the new digest when the run ends.
func ResetBuildStatus(status *geassv1alpha1.GeassBuildStatus) {
	status.Phase = geassv1alpha1.GeassBuildPending
	status.CancelRequested = false
	status.FailureReason = ""
	status.StartedAt = nil
	status.CompletedAt = nil
	status.ImageDigest = ""
	status.LogRef = ""
	status.RetryCount = 0
	status.SourceRevision = ""
	status.CIState = ""
	status.CIChecks = nil
}

// NewestBuild is the build that was created last, which is the one for the service's current settings: a
// build is named by a hash of its settings, so a change to them makes a newer build. A tie goes to the name
// that sorts last, so the answer does not depend on the order of the list.
func NewestBuild(builds []geassv1alpha1.GeassBuild) *geassv1alpha1.GeassBuild {
	var newest *geassv1alpha1.GeassBuild
	for i := range builds {
		if newest == nil || builds[i].CreationTimestamp.After(newest.CreationTimestamp.Time) ||
			(builds[i].CreationTimestamp.Equal(&newest.CreationTimestamp) && builds[i].Name > newest.Name) {
			newest = &builds[i]
		}
	}
	return newest
}

var (
	imageDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	// Kaniko says "Pushed <repository>@sha256:<digest>" when the push is done.
	kanikoPushedPattern = regexp.MustCompile(`Pushed \S+@(sha256:[a-f0-9]{64})`)
)

// ValidImageDigest reports whether s is a sha256 image digest, as a registry returns it.
func ValidImageDigest(s string) bool { return imageDigestPattern.MatchString(s) }

// DigestFromKanikoLog finds the digest of the pushed image in Kaniko's output. Kaniko prints it as part of
// "Pushed <repository>@sha256:<digest>", so it is not a word of its own and a search for a word that starts
// with sha256: finds nothing. The last push wins. Other digests in the output belong to base images and
// layers, which is why only this line is read.
func DigestFromKanikoLog(log string) string {
	matches := kanikoPushedPattern.FindAllStringSubmatch(log, -1)
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1][1]
}

// ImageReferenceWithDigest is the image to run for a built image: the repository of image, whatever tag it has,
// with the digest after it (repository@sha256:...). A digest on its own is not a reference a node can pull,
// and a reference by digest keeps meaning the image that was built when the tag is moved by a later build.
func ImageReferenceWithDigest(image, digest string) (string, error) {
	if !ValidImageDigest(digest) {
		return "", fmt.Errorf("%q is not an image digest", digest)
	}
	repository := ImageRepository(image)
	if repository == "" {
		return "", fmt.Errorf("the build has no image repository to pull %s from", digest)
	}
	return repository + "@" + digest, nil
}

// ImageRepository is an image reference without its tag and without its digest. A colon in the registry
// host (a port) is not a tag, so only a colon after the last slash is taken to start one.
func ImageRepository(image string) string {
	image = strings.TrimSpace(image)
	if at := strings.Index(image, "@"); at >= 0 {
		image = image[:at]
	}
	if colon := strings.LastIndex(image, ":"); colon > strings.LastIndex(image, "/") {
		image = image[:colon]
	}
	return image
}

// NormalizeRepoPath cleans a path inside a repository checkout: the dashboard's "Root directory" and the
// Dockerfile path. A leading slash means the root of the repository, as the dashboard's examples write it
// (/apps/api), and "." or an empty path is the root itself, which is returned as "". A path that leaves the
// checkout is an error.
func NormalizeRepoPath(p string) (string, error) {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	if slices.Contains(strings.Split(p, "/"), "..") {
		return "", fmt.Errorf("%q leaves the repository", p)
	}
	p = strings.TrimLeft(path.Clean("/"+p), "/")
	return p, nil
}

// BuildPaths says where Kaniko builds, as absolute paths in the checkout at workspace: the build context
// and the Dockerfile. The Dockerfile is relative to the root directory (the context), as the GeassApp API
// documents it. A Dockerfile path that already starts with the root directory is taken as relative to the
// repository: before the root directory was used, the only way to build a subdirectory was to write the
// Dockerfile path in full, and services set up that way go on building.
func BuildPaths(workspace, root, dockerfile string) (contextDir, dockerfilePath string, err error) {
	root, err = NormalizeRepoPath(root)
	if err != nil {
		return "", "", fmt.Errorf("root directory: %w", err)
	}
	dockerfile, err = NormalizeRepoPath(dockerfile)
	if err != nil {
		return "", "", fmt.Errorf("path of the Dockerfile: %w", err)
	}
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	contextDir = path.Join(workspace, root)
	if root != "" && strings.HasPrefix(dockerfile, root+"/") {
		return contextDir, path.Join(workspace, dockerfile), nil
	}
	return contextDir, path.Join(contextDir, dockerfile), nil
}

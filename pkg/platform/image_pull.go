package platform

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	// MessageCouldNotPullImage starts what a database says when the image its server runs from cannot be pulled. A
	// server that cannot get its image never becomes ready, and from outside that looks like one that is slow to
	// start, so without this the database said "Service is starting" for as long as it took someone to look at the pod.
	MessageCouldNotPullImage = "Could not pull the database image"

	// ImagePullFailureMaxDuration is how long a database's image may go unpulled before the database is given up on.
	// The kubelet waits between pulls, doubling from ten seconds to five minutes, so the five tries thirty seconds apart
	// that end other failures (ReconcileFailureThreshold) would end a registry that was away for two minutes. A pull that
	// is only slow is not a failure and never gets here: it is ContainerCreating until it fails. One that has failed for
	// this long is a missing image or a registry that cannot be reached, and the database says so as a failure, and not
	// as one that is starting.
	ImagePullFailureMaxDuration = 10 * time.Minute
)

// ImagePullMessage says that a server's image cannot be pulled, and why Kubernetes says it cannot: ErrImagePull
// while a pull is failing, ImagePullBackOff while the kubelet waits to try again, or InvalidImageName.
func ImagePullMessage(image, reason string) string {
	return fmt.Sprintf("%s %s (%s)", MessageCouldNotPullImage, image, reason)
}

// IsImagePullFailure reports whether a not-ready message is ImagePullMessage's.
func IsImagePullFailure(message string) bool {
	return strings.HasPrefix(strings.TrimSpace(message), MessageCouldNotPullImage)
}

// reconcileFailureLimits are how many failures in a row, and for how long, may go on before retries stop. A failure
// that the kubelet is already retrying on its own schedule is given the longer time and no count: counting passes
// would end it at the pace of the controller's requeue, which is not the pace of the pulls.
func reconcileFailureLimits(message string) (failures int32, duration time.Duration) {
	if IsImagePullFailure(message) {
		return math.MaxInt32, ImagePullFailureMaxDuration
	}
	return ReconcileFailureThreshold, ReconcileFailureMaxDuration
}

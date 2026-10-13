package crdcompat

import (
	"fmt"
	"os"
	"strings"

	"sigs.k8s.io/yaml"
)

// An Exception lets one violation through, because the change is deliberate and its cost has been accepted:
// a field that was never used, say, or a kind that has been retired along the way the release notes describe.
// It has to say why. An exception is a record in review that someone decided that a cluster owner may lose
// something, and a reviewer cannot weigh a decision that gives no reason.
type Exception struct {
	// CRD, Version, Path and Rule pick out the violation. Version may be left empty to match every version.
	CRD     string `json:"crd"`
	Version string `json:"version,omitempty"`
	Path    string `json:"path,omitempty"`
	Rule    string `json:"rule"`
	// Reason says why the change is safe or acceptable, and what happened to the data.
	Reason string `json:"reason"`
}

// Exceptions is the contents of the exceptions file.
type Exceptions struct {
	Exceptions []Exception `json:"exceptions"`
}

// LoadExceptions reads the exceptions file. A file that does not exist is no exceptions.
func LoadExceptions(path string) (Exceptions, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Exceptions{}, nil
	}
	if err != nil {
		return Exceptions{}, err
	}
	var ex Exceptions
	if err := yaml.UnmarshalStrict(data, &ex); err != nil {
		return Exceptions{}, fmt.Errorf("%s: %w", path, err)
	}
	for i, e := range ex.Exceptions {
		switch {
		case e.CRD == "" || e.Rule == "":
			return Exceptions{}, fmt.Errorf("%s: exception %d needs a crd and a rule", path, i+1)
		case strings.TrimSpace(e.Reason) == "":
			return Exceptions{}, fmt.Errorf("%s: the exception for %s %s %s has no reason, and an exception without one cannot be reviewed", path, e.CRD, e.Path, e.Rule)
		}
	}
	return ex, nil
}

func (e Exception) matches(v Violation) bool {
	return e.CRD == v.CRD && e.Rule == v.Rule && e.Path == v.Path && (e.Version == "" || e.Version == v.Version)
}

// Apply removes the violations that an exception covers. It also returns the exceptions that covered nothing:
// the change they were for has since been undone or released, and a stale exception would quietly let the
// same mistake through later.
func (e Exceptions) Apply(violations []Violation) (remaining []Violation, unused []Exception) {
	used := make([]bool, len(e.Exceptions))
	for _, v := range violations {
		covered := false
		for i, ex := range e.Exceptions {
			if ex.matches(v) {
				used[i] = true
				covered = true
			}
		}
		if !covered {
			remaining = append(remaining, v)
		}
	}
	for i, ex := range e.Exceptions {
		if !used[i] {
			unused = append(unused, ex)
		}
	}
	return remaining, unused
}

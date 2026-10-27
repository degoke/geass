package projectbundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

var (
	// ErrNotABundle is what an upload that is not a Geass bundle gets.
	ErrNotABundle = errors.New("this is not a Geass project bundle")
	// ErrTooLarge is what an archive, or what is in it, that is over the limits gets.
	ErrTooLarge = errors.New("the bundle is larger than the limits allow")
	// ErrNewer is what a bundle made by a later format gets.
	ErrNewer = errors.New("the bundle was made in a newer format than this Geass reads: upgrade Geass, or export the project again from a release that matches")
	// ErrCorrupt is what a bundle whose files do not match its manifest gets.
	ErrCorrupt = errors.New("the bundle is damaged: a file does not match its checksum in the manifest")
)

// InvalidError is a bundle that reads as one and holds something that it must not.
type InvalidError struct{ Problems []string }

func (e *InvalidError) Error() string {
	return "the bundle is not valid: " + strings.Join(e.Problems, "; ")
}

// filePath matches the paths a bundle may hold: lowercase names in at most two directories.
var filePath = regexp.MustCompile(`^(geass-bundle\.json|project\.yaml|(services|databases|object-stores)/[a-z0-9]([-a-z0-9]*[a-z0-9])?/[a-z0-9]([-a-z0-9.]*[a-z0-9])?\.yaml)$`)

func sum(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

type file struct {
	path string
	kind string
	id   string
	data []byte
}

// Encode writes the bundle as a gzip-compressed tar. The same bundle always gives the same bytes: files are in path order and
// carry no times, owners or permissions that vary. The manifest's entries are made here, from the files, so they cannot
// disagree with them.
func (b *Bundle) Encode() ([]byte, error) {
	var files []file
	add := func(path, kind, id string, doc any) error {
		data, err := yaml.Marshal(doc)
		if err != nil {
			return fmt.Errorf("could not write %s: %w", path, err)
		}
		files = append(files, file{path: path, kind: kind, id: id, data: data})
		return nil
	}
	if err := add(ProjectFile, KindProject, b.Project.Metadata.Name, b.Project); err != nil {
		return nil, err
	}
	for _, doc := range b.Services {
		if err := add(docPath(dirServices, string(doc.Spec.Environment), doc.Metadata.Name), KindService, doc.Metadata.Name, doc); err != nil {
			return nil, err
		}
	}
	for _, doc := range b.Databases {
		if err := add(docPath(dirDatabases, string(doc.Spec.Environment), doc.Metadata.Name), KindDatabase, doc.Metadata.Name, doc); err != nil {
			return nil, err
		}
	}
	for _, doc := range b.Buckets {
		if err := add(docPath(dirBuckets, string(doc.Spec.Environment), doc.Metadata.Name), KindBucket, doc.Metadata.Name, doc); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(files, func(a, c file) int { return strings.Compare(a.path, c.path) })

	manifest := b.Manifest
	manifest.Format, manifest.Version = Format, Version
	manifest.Secrets.Mode = SecretsExcluded
	manifest.Entries = make([]Entry, 0, len(files))
	for _, f := range files {
		manifest.Entries = append(manifest.Entries, Entry{Path: f.path, Kind: f.kind, ID: f.id, Size: int64(len(f.data)), SHA256: sum(f.data)})
	}
	manifest.ToReenter = nonNilItems(manifest.ToReenter)
	manifest.NotIncluded = nonNilItems(manifest.NotIncluded)
	if manifest.Data == nil {
		manifest.Data = []DataNote{}
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("could not write the manifest: %w", err)
	}

	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	write := func(path string, data []byte) error {
		header := &tar.Header{Name: path, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0), Format: tar.FormatPAX}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	if err := write(ManifestName, manifestJSON); err != nil {
		return nil, err
	}
	for _, f := range files {
		if err := write(f.path, f.data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func nonNilItems(items []Item) []Item {
	if items == nil {
		return []Item{}
	}
	return items
}

func docPath(dir, environment, id string) string { return dir + "/" + environment + "/" + id + ".yaml" }

// Decode reads and checks a bundle. It reads at most the limits' worth, never writes anything to disk, refuses an archive
// that holds anything but plain files with the names a bundle has, checks every file against the manifest, reads every
// definition strictly (a field Geass does not know is an error), and then runs Validate.
func Decode(r io.Reader, limits Limits) (*Bundle, error) {
	limited := &io.LimitedReader{R: r, N: limits.MaxArchiveBytes + 1}
	gz, err := gzip.NewReader(limited)
	if err != nil {
		return nil, ErrNotABundle
	}
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	var expanded int64
	first := true
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if limited.N <= 0 {
				return nil, ErrTooLarge
			}
			return nil, ErrNotABundle
		}
		if first && header.Name != ManifestName {
			return nil, ErrNotABundle
		}
		first = false
		if header.Typeflag != tar.TypeReg {
			return nil, &InvalidError{Problems: []string{fmt.Sprintf("%q is not a plain file", header.Name)}}
		}
		if !filePath.MatchString(header.Name) {
			return nil, &InvalidError{Problems: []string{fmt.Sprintf("%q is not a file a bundle holds", header.Name)}}
		}
		if _, dup := files[header.Name]; dup {
			return nil, &InvalidError{Problems: []string{fmt.Sprintf("%q is in the bundle twice", header.Name)}}
		}
		if len(files) >= limits.MaxFiles || header.Size > limits.MaxFileBytes {
			return nil, ErrTooLarge
		}
		data, err := io.ReadAll(io.LimitReader(tr, limits.MaxFileBytes+1))
		if err != nil {
			if limited.N <= 0 {
				return nil, ErrTooLarge
			}
			return nil, ErrNotABundle
		}
		if int64(len(data)) > limits.MaxFileBytes {
			return nil, ErrTooLarge
		}
		expanded += int64(len(data))
		if expanded > limits.MaxExpandedBytes {
			return nil, ErrTooLarge
		}
		files[header.Name] = data
	}
	if limited.N <= 0 {
		return nil, ErrTooLarge
	}
	manifestData, ok := files[ManifestName]
	if !ok {
		return nil, ErrNotABundle
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil || manifest.Format != Format {
		return nil, ErrNotABundle
	}
	if manifest.Version > Version {
		return nil, ErrNewer
	}
	if manifest.Version < 1 {
		return nil, ErrNotABundle
	}
	if manifest.Secrets.Mode != SecretsExcluded {
		return nil, &InvalidError{Problems: []string{fmt.Sprintf("secrets are %q in this bundle, and this Geass reads only bundles without secret values", manifest.Secrets.Mode)}}
	}

	listed := map[string]bool{}
	bundle := &Bundle{Manifest: manifest}
	for _, entry := range manifest.Entries {
		data, ok := files[entry.Path]
		if !ok || entry.Path == ManifestName {
			return nil, &InvalidError{Problems: []string{fmt.Sprintf("the manifest lists %q, which is not in the bundle", entry.Path)}}
		}
		if listed[entry.Path] {
			return nil, &InvalidError{Problems: []string{fmt.Sprintf("the manifest lists %q twice", entry.Path)}}
		}
		listed[entry.Path] = true
		if int64(len(data)) != entry.Size || sum(data) != entry.SHA256 {
			return nil, ErrCorrupt
		}
		if err := bundle.read(entry.Path, data); err != nil {
			return nil, err
		}
	}
	for path := range files {
		if path != ManifestName && !listed[path] {
			return nil, &InvalidError{Problems: []string{fmt.Sprintf("%q is in the bundle but not in its manifest", path)}}
		}
	}
	if bundle.Project.Kind == "" {
		return nil, &InvalidError{Problems: []string{ProjectFile + " is missing"}}
	}
	if err := bundle.Validate(); err != nil {
		return nil, err
	}
	return bundle, nil
}

// read parses one file into the bundle, by where it is. A path that says one environment and a definition that says another
// is a bundle that was edited, and is refused by Validate.
func (b *Bundle) read(path string, data []byte) error {
	bad := func(err error) error {
		return &InvalidError{Problems: []string{fmt.Sprintf("%s: %v", path, err)}}
	}
	// where is what the path says a definition is: its environment and name.
	where := func(name string, environment geassv1alpha1.GeassEnvironment) error {
		if path != docPath(strings.SplitN(path, "/", 2)[0], string(environment), name) {
			return bad(errors.New("the file is not where its name and environment say it should be"))
		}
		return nil
	}
	switch {
	case path == ProjectFile:
		return decodeInto(data, &b.Project, bad)
	case strings.HasPrefix(path, dirServices+"/"):
		var doc Document[geassv1alpha1.GeassAppSpec]
		if err := decodeInto(data, &doc, bad); err != nil {
			return err
		}
		if err := where(doc.Metadata.Name, doc.Spec.Environment); err != nil {
			return err
		}
		b.Services = append(b.Services, doc)
	case strings.HasPrefix(path, dirDatabases+"/"):
		var doc Document[geassv1alpha1.GeassDatabaseSpec]
		if err := decodeInto(data, &doc, bad); err != nil {
			return err
		}
		if err := where(doc.Metadata.Name, doc.Spec.Environment); err != nil {
			return err
		}
		b.Databases = append(b.Databases, doc)
	case strings.HasPrefix(path, dirBuckets+"/"):
		var doc Document[geassv1alpha1.GeassObjectStoreSpec]
		if err := decodeInto(data, &doc, bad); err != nil {
			return err
		}
		if err := where(doc.Metadata.Name, doc.Spec.Environment); err != nil {
			return err
		}
		b.Buckets = append(b.Buckets, doc)
	}
	return nil
}

func decodeInto(data []byte, into any, bad func(error) error) error {
	if err := yaml.UnmarshalStrict(data, into); err != nil {
		return bad(err)
	}
	return nil
}

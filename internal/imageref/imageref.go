// Package imageref parses the image references containers name and decides,
// from the digests Docker recorded for a running image and a registry lookup
// of the same reference, whether a newer image has been published for it.
//
// The registry connector, the engine (which files one change-feed entry per
// newly available update) and the services API all use Compare, so the badge
// in the ledger and the change feed can never disagree about a container.
package imageref

import (
	"errors"
	"regexp"
	"strings"

	"github.com/distribution/reference"
	"github.com/opencontainers/go-digest"
)

// Per-container update statuses reported by the services API.
const (
	UpToDate        = "up_to_date"
	UpdateAvailable = "update_available"
	Unknown         = "unknown"
	Pinned          = "pinned"
)

// Registry lookup outcomes stored per image reference.
const (
	LookupResolved = "resolved"
	LookupPinned   = "pinned"
	LookupUnknown  = "unknown"
)

// Ref is a parsed, fully qualified image reference.
type Ref struct {
	Domain string // canonical registry domain, "docker.io" for Docker Hub
	Path   string // repository path, "library/nginx" for "nginx"
	Tag    string // "latest" when the reference names none
	Digest string // set when the reference pins a digest
}

// Repository is the fully qualified repository name, e.g. docker.io/library/nginx.
func (r Ref) Repository() string { return r.Domain + "/" + r.Path }

// Key identifies what a registry lookup of r returns, so two spellings of one
// image ("nginx:1.27" and "docker.io/library/nginx:1.27") share a lookup.
func (r Ref) Key() string {
	if r.Digest != "" {
		return r.Repository() + "@" + r.Digest
	}
	return r.Repository() + ":" + r.Tag
}

// ErrImageID reports a container that names an image ID instead of a
// reference: there is no tag to look up.
var ErrImageID = errors.New("the container names an image ID, not a tag")

var imageID = regexp.MustCompile(`^(sha256:)?[a-f0-9]{12,64}$`)

// Join rebuilds the reference a container names from the image and tag the
// connectors store. A digest-pinned reference keeps its digest in the tag
// column ("sha256:..."), so it is joined back with "@".
func Join(image, tag string) string {
	image, tag = strings.TrimSpace(image), strings.TrimSpace(tag)
	switch {
	case image == "":
		return ""
	case tag == "":
		return image
	case strings.Contains(tag, ":"):
		return image + "@" + tag
	default:
		return image + ":" + tag
	}
}

// Parse normalizes a reference the way Docker does: "nginx" is
// docker.io/library/nginx:latest.
func Parse(ref string) (Ref, error) {
	ref = strings.TrimSpace(ref)
	if imageID.MatchString(ref) {
		return Ref{}, ErrImageID
	}
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return Ref{}, err
	}
	out := Ref{Domain: reference.Domain(named), Path: reference.Path(named), Tag: "latest"}
	if out.Domain == "registry-1.docker.io" || out.Domain == "index.docker.io" {
		out.Domain = "docker.io"
	}
	if tagged, ok := named.(reference.Tagged); ok {
		out.Tag = tagged.Tag()
	}
	if digested, ok := named.(reference.Digested); ok {
		out.Digest = digested.Digest().String()
	}
	return out, nil
}

// ValidDigest reports whether value is a well-formed content digest such as
// "sha256:<64 hex>".
func ValidDigest(value string) bool {
	d, err := digest.Parse(value)
	return err == nil && d.Validate() == nil
}

// RunningDigests returns the digests Docker recorded for the running image
// ("repo@sha256:...") that belong to ref's repository, in input order. For a
// multi-arch image this is the index digest, which is what the tag resolves
// to in the registry; for a single-arch image it is the manifest digest.
func RunningDigests(ref Ref, repoDigests []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, value := range repoDigests {
		parsed, err := Parse(value)
		if err != nil || parsed.Digest == "" || parsed.Repository() != ref.Repository() || seen[parsed.Digest] {
			continue
		}
		seen[parsed.Digest] = true
		out = append(out, parsed.Digest)
	}
	return out
}

// Lookup is the stored registry answer for one reference.
type Lookup struct {
	Outcome      string // LookupResolved, LookupPinned or LookupUnknown
	RemoteDigest string // what the tag resolves to, when resolved
	Reason       string // why the outcome is unknown, or why the last check failed
}

// Result is one container's update status.
type Result struct {
	Status  string
	Running string // the running digest compared, when there is one
	Reason  string
}

// Compare decides one container's status. It never guesses: a container whose
// running image has no registry digest for the same repository (built
// locally, pulled from a private registry, or a Docker source that cannot
// list images) is unknown, as is any reference the registry did not resolve.
func Compare(image, tag string, repoDigests []string, lookup Lookup) Result {
	ref, err := Parse(Join(image, tag))
	if err != nil {
		if errors.Is(err, ErrImageID) {
			return Result{Status: Unknown, Reason: "The container names an image ID, not a tag."}
		}
		return Result{Status: Unknown, Reason: "The image reference could not be parsed."}
	}
	if ref.Digest != "" || lookup.Outcome == LookupPinned {
		return Result{Status: Pinned, Reason: "The container runs an image pinned by digest."}
	}
	if lookup.Outcome != LookupResolved || !ValidDigest(lookup.RemoteDigest) {
		reason := lookup.Reason
		if reason == "" {
			reason = "The registry did not report a digest for this tag."
		}
		return Result{Status: Unknown, Reason: reason}
	}
	running := RunningDigests(ref, repoDigests)
	if len(running) == 0 {
		return Result{Status: Unknown, Reason: "Docker recorded no registry digest for the running image (built locally, or the Docker source cannot list images)."}
	}
	for _, d := range running {
		if d == lookup.RemoteDigest {
			return Result{Status: UpToDate, Running: d, Reason: lookup.Reason}
		}
	}
	return Result{Status: UpdateAvailable, Running: running[0], Reason: lookup.Reason}
}

// Short abbreviates a digest for display: "sha256:" plus 12 hex characters.
func Short(value string) string {
	algorithm, hex, ok := strings.Cut(value, ":")
	if !ok || len(hex) <= 12 {
		return value
	}
	return algorithm + ":" + hex[:12]
}

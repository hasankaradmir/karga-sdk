package resource

import (
	"fmt"
	"regexp"
	"strings"
)

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// IsDNSLabel reports whether s is a valid lowercase RFC 1123 label.
func IsDNSLabel(s string) bool { return dnsLabel.MatchString(s) }

// ValidateReleaseName checks a release name: a DNS label that is not karga's own. Runtime objects
// are named <release>-<name>, so a release named "karga" or "karga-…" could take the names of
// karga's objects (the host gateway is karga-gateway). Every request naming a release is checked,
// because the name also locates the release's state on disk.
func ValidateReleaseName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("a release name is required")
	case !IsDNSLabel(name):
		return fmt.Errorf("release name %q must be a DNS label (lowercase letters, digits and '-', at most 63)", name)
	case name == strings.TrimSuffix(ReservedPrefix, "-") || strings.HasPrefix(name, ReservedPrefix):
		return fmt.Errorf("release name %q: names starting with %q are reserved for karga", name, strings.TrimSuffix(ReservedPrefix, "-"))
	}
	return nil
}

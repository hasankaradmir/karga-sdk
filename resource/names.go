package resource

import "regexp"

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// IsDNSLabel reports whether s is a valid lowercase RFC 1123 label.
func IsDNSLabel(s string) bool { return dnsLabel.MatchString(s) }

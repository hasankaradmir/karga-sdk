package packages

import (
	"errors"
	"fmt"
)

// LintReleaseName is the release name used when lint renders a package.
const LintReleaseName = "lint"

// Lint loads and renders the package at path and returns every problem found, and the warnings of
// a package without problems (Manifest.Warnings).
func Lint(path string, opts Options) (problems, warnings []string) {
	p, err := Load(path)
	if err != nil {
		return flatten(err), nil
	}
	if opts.ReleaseName == "" {
		opts.ReleaseName = LintReleaseName
	}
	m, err := Render(p, opts)
	if err != nil {
		return flatten(err), nil
	}
	return nil, m.Warnings()
}

// flatten turns an errors.Join into one message per joined error.
func flatten(err error) []string {
	multi, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return []string{err.Error()}
	}
	var out []string
	for _, e := range multi.Unwrap() {
		out = append(out, flatten(e)...)
	}
	return out
}

// prefixed adds prefix to every error joined in err, keeping them separate for flatten.
func prefixed(prefix string, err error) error {
	multi, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	var out []error
	for _, e := range multi.Unwrap() {
		out = append(out, prefixed(prefix, e))
	}
	return errors.Join(out...)
}

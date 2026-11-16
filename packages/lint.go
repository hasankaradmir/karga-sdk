package packages

import (
	"errors"
	"fmt"
)

// LintReleaseName is the release name used when lint renders a package.
const LintReleaseName = "lint"

// Lint loads and renders the package at path and returns every problem found.
func Lint(path string, opts Options) []string {
	p, err := Load(path)
	if err != nil {
		return flatten(err)
	}
	if opts.ReleaseName == "" {
		opts.ReleaseName = LintReleaseName
	}
	if _, err := Render(p, opts); err != nil {
		return flatten(err)
	}
	return nil
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

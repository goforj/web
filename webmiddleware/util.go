package webmiddleware

import "errors"

// invalidConfigError wraps middleware configuration failures consistently.
func invalidConfigError(message string) error {
	return errors.New("web: " + message)
}

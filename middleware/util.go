package middleware

import "errors"

func invalidConfigError(message string) error {
	return errors.New("web: " + message)
}

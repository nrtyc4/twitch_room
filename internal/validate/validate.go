// Package validate centralises the identifier rules applied to user-supplied
// input. Twitch logins are limited to letters, digits and underscore, so
// accepting anything else lets callers stuff control characters, slashes and
// path fragments into identifiers that are later persisted and echoed back.
// These helpers keep every identifier short, plain ASCII and free of URL
// syntax so it is always safe to use in paths, URLs and storage keys.
package validate

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidUser   = errors.New("invalid user id")
	ErrInvalidStream = errors.New("invalid stream id")

	// MaxIDLength caps identifiers: user IDs (Twitch logins), publisher IDs
	// and stream IDs must fit in a short plain-ASCII name.
	MaxIDLength = 100
)

// UserID reports whether id is acceptable as a user, publisher or subscriber
// identifier: 1-100 chars of letters, digits, underscore or hyphen.
func UserID(id string) error {
	if err := identifier(id); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidUser, err)
	}
	return nil
}

// StreamID reports whether id is acceptable as a stream ID: the same name
// rule as user IDs after URL forms have been normalised to a channel login.
func StreamID(id string) error {
	if err := identifier(id); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidStream, err)
	}
	return nil
}

func identifier(s string) error {
	if len(s) == 0 {
		return errors.New("empty")
	}
	if len(s) > MaxIDLength {
		return fmt.Errorf("longer than %d characters", MaxIDLength)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '_' || c == '-':
		default:
			return fmt.Errorf("contains disallowed character %q (allowed: letters, digits, '_', '-')", c)
		}
	}
	return nil
}

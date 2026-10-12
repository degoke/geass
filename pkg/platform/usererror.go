package platform

import (
	"errors"
	"fmt"
)

// UserError is an error whose text is written for end users and shown in the dashboard as it is.
// A named type lets these sentences stay capitalised without tripping the style check on
// errors.New and fmt.Errorf, which assumes error text is a fragment meant for developers.
type UserError string

func (e UserError) Error() string { return string(e) }

// IsUserError reports whether err is, or wraps, a UserError.
func IsUserError(err error) bool {
	var userErr UserError
	return errors.As(err, &userErr)
}

// Userf formats a UserError.
func Userf(format string, args ...any) error {
	return UserError(fmt.Sprintf(format, args...))
}

package run

import "errors"

type ValidationError struct {
	Err error
}

func (e ValidationError) Error() string {
	if e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e ValidationError) Unwrap() error {
	return e.Err
}

func validationError(err error) error {
	if err == nil {
		return nil
	}
	var existing ValidationError
	if errors.As(err, &existing) {
		return err
	}
	return ValidationError{Err: err}
}

func IsValidationError(err error) bool {
	var validationErr ValidationError
	return errors.As(err, &validationErr)
}

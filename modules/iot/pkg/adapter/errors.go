package adapter

import "errors"

type permanentMessageError struct {
	err error
}

func (err permanentMessageError) Error() string {
	return err.err.Error()
}

func (err permanentMessageError) Unwrap() error {
	return err.err
}

func permanentMessage(err error) error {
	if err == nil {
		return nil
	}
	return permanentMessageError{err: err}
}

func isPermanentMessageError(err error) bool {
	var target permanentMessageError
	return errors.As(err, &target)
}

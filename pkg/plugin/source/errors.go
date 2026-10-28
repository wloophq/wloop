package source

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func toStatus(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	code := codes.Unknown
	switch {
	case errors.Is(err, ErrNotFound):
		code = codes.NotFound
	case errors.Is(err, ErrUnavailable):
		code = codes.Unavailable
	case errors.Is(err, ErrUnsupported):
		code = codes.Unimplemented
	case errors.Is(err, ErrUnfit):
		code = codes.FailedPrecondition
	case errors.Is(err, context.Canceled):
		code = codes.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		code = codes.DeadlineExceeded
	}
	return status.Error(code, err.Error())
}

func fromStatus(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	switch st.Code() {
	case codes.NotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, st.Message())
	case codes.Unavailable:
		return fmt.Errorf("%w: %s", ErrUnavailable, st.Message())
	case codes.Unimplemented:
		return fmt.Errorf("%w: %s", ErrUnsupported, st.Message())
	case codes.FailedPrecondition:
		return fmt.Errorf("%w: %s", ErrUnfit, st.Message())
	case codes.Canceled:
		return fmt.Errorf("%w: %s", context.Canceled, st.Message())
	case codes.DeadlineExceeded:
		return fmt.Errorf("%w: %s", context.DeadlineExceeded, st.Message())
	}
	return err
}

func unsupported(what string) error {
	return status.Errorf(codes.Unimplemented, "the plugin is no %s", what)
}

package source

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// answeredReason marks a status the other side answered with, as opposed
// to one gRPC made when no answer came.
const answeredReason = "answered"

func toStatus(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	code := codes.Unknown
	switch {
	case errors.Is(err, ErrMayHavePosted):
		code = codes.DataLoss
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
	st := status.New(code, err.Error())
	if d, derr := st.WithDetails(&errdetails.ErrorInfo{Reason: answeredReason}); derr == nil {
		st = d
	}
	return st.Err()
}

// answered says whether st came from the other side's handler.
func answered(st *status.Status) bool {
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.GetReason() == answeredReason {
			return true
		}
	}
	return false
}

// mayHavePosted is a failure the other side says may have posted: its
// text, which names it.
type mayHavePosted struct{ msg string }

func (e mayHavePosted) Error() string { return e.msg }

func (e mayHavePosted) Is(target error) bool { return target == ErrMayHavePosted }

func fromStatus(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	if !answered(st) {
		switch st.Code() {
		case codes.Canceled:
			return fmt.Errorf("%w: %w: %s", ErrNoAnswer, context.Canceled, st.Message())
		case codes.DeadlineExceeded:
			return fmt.Errorf("%w: %w: %s", ErrNoAnswer, context.DeadlineExceeded, st.Message())
		case codes.Unimplemented:
		default:
			return fmt.Errorf("%w: %w: %s", ErrNoAnswer, ErrUnavailable, st.Message())
		}
	}
	switch st.Code() {
	case codes.DataLoss:
		msg := st.Message()
		if !strings.HasPrefix(msg, ErrMayHavePosted.Error()) {
			msg = ErrMayHavePosted.Error() + ": " + msg
		}
		return mayHavePosted{msg}
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

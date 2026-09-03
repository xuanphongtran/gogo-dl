package apperror

import (
	"errors"
	"net/http"

	"connectrpc.com/connect"
)

func ToConnect(err error) *connect.Error {
	if err == nil {
		return nil
	}
	var appErr *AppError
	if errors.As(err, &appErr) {
		code := httpToConnectCode(appErr.Code)
		return connect.NewError(code, errors.New(appErr.Message))
	}
	return connect.NewError(connect.CodeInternal, ErrInternal)
}

func httpToConnectCode(httpCode int) connect.Code {
	switch httpCode {
	case http.StatusBadRequest:
		return connect.CodeInvalidArgument
	case http.StatusUnauthorized:
		return connect.CodeUnauthenticated
	case http.StatusForbidden:
		return connect.CodePermissionDenied
	case http.StatusNotFound:
		return connect.CodeNotFound
	case http.StatusConflict:
		return connect.CodeAlreadyExists
	case http.StatusUnprocessableEntity:
		return connect.CodeInvalidArgument
	case http.StatusTooManyRequests:
		return connect.CodeResourceExhausted
	case http.StatusNotImplemented:
		return connect.CodeUnimplemented
	case http.StatusServiceUnavailable:
		return connect.CodeUnavailable
	case http.StatusGatewayTimeout:
		return connect.CodeDeadlineExceeded
	case http.StatusInternalServerError:
		return connect.CodeInternal
	default:
		if httpCode >= 400 && httpCode < 500 {
			return connect.CodeInvalidArgument
		}
		return connect.CodeInternal
	}
}

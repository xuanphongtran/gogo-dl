// Package apperror defines application-level error types and HTTP response helpers.
package apperror

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// AppError is a structured error that carries an HTTP status code and a
// user-facing message. Business logic should return AppError; handlers
// call Respond() to write the JSON response.
type AppError struct {
	Code    int    `json:"-"`
	Message string `json:"error"`
}

func (e *AppError) Error() string { return e.Message }

// Common errors
var (
	ErrUnauthorized        = &AppError{Code: http.StatusUnauthorized, Message: "unauthorized"}
	ErrForbidden           = &AppError{Code: http.StatusForbidden, Message: "forbidden"}
	ErrNotFound            = &AppError{Code: http.StatusNotFound, Message: "not found"}
	ErrConflict            = &AppError{Code: http.StatusConflict, Message: "resource already exists"}
	ErrAccountOwnsRooms    = &AppError{Code: http.StatusConflict, Message: "cannot delete account while owning rooms"}
	ErrBadRequest          = &AppError{Code: http.StatusBadRequest, Message: "bad request"}
	ErrInvalidRequest      = &AppError{Code: http.StatusBadRequest, Message: "invalid request"}
	ErrRequestBodyTooLarge = &AppError{Code: http.StatusRequestEntityTooLarge, Message: "request body too large"}
	ErrOriginForbidden     = &AppError{Code: http.StatusForbidden, Message: "origin forbidden"}
	ErrRateLimited         = &AppError{Code: http.StatusTooManyRequests, Message: "rate limit exceeded"}
	ErrInternal            = &AppError{Code: http.StatusInternalServerError, Message: "internal server error"}
)

// New creates a new AppError with the given status code and message.
func New(code int, message string) *AppError {
	return &AppError{Code: code, Message: message}
}

// Respond writes the AppError (or a generic 500) as a JSON response and aborts the gin chain.
func Respond(c *gin.Context, err error) {
	var appErr *AppError
	if errors.As(err, &appErr) {
		c.AbortWithStatusJSON(appErr.Code, appErr)
		return
	}
	c.AbortWithStatusJSON(http.StatusInternalServerError, ErrInternal)
}

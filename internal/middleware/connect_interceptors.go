package middleware

import (
	"context"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog/log"

	"github.com/xuanphongtran/gogo-dl/internal/config"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

type connectCtxKey string

const connectCtxUserIDKey connectCtxKey = "userID"

func SetUserIDConnect(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, connectCtxUserIDKey, userID)
}

func MustGetUserIDConnect(ctx context.Context) int64 {
	v, ok := ctx.Value(connectCtxUserIDKey).(int64)
	if !ok {
		panic("middleware: Connect auth interceptor not applied for this RPC")
	}
	return v
}

var connectPublicProcedures = map[string]struct{}{
	"/api.v1.UserService/Register":      {},
	"/api.v1.UserService/Login":         {},
	"/api.v1.UserService/RefreshTokens": {},
}

func ConnectAuthInterceptor(cfg *config.Config) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			proc := req.Spec().Procedure
			if _, public := connectPublicProcedures[proc]; public {
				return next(ctx, req)
			}
			tokenStr := extractConnectToken(req)
			if tokenStr == "" {
				return nil, apperror.ToConnect(apperror.ErrUnauthorized)
			}
			claims, err := ParseAccessToken(cfg, tokenStr)
			if err != nil {
				return nil, apperror.ToConnect(apperror.ErrUnauthorized)
			}
			ctx = SetUserIDConnect(ctx, claims.UserID)
			return next(ctx, req)
		}
	}
}

func extractConnectToken(req connect.AnyRequest) string {
	authHeader := req.Header().Get("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
			return parts[1]
		}
	}
	return ""
}

func ConnectLoggerInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			start := time.Now()
			proc := req.Spec().Procedure
			proto := req.Peer().Protocol
			addr := req.Peer().Addr
			resp, err := next(ctx, req)
			latency := time.Since(start)
			ev := log.Info()
			if err != nil {
				var cerr *connect.Error
				if errorsAsConnect(err, &cerr) {
					switch cerr.Code() {
					case connect.CodeInternal, connect.CodeUnavailable, connect.CodeDataLoss:
						ev = log.Error()
					default:
						ev = log.Warn()
					}
					ev.Str("code", cerr.Code().String())
				} else {
					ev = log.Error()
				}
				ev.Err(err)
			} else {
				ev.Str("code", "ok")
			}
			ev.
				Str("procedure", proc).
				Str("protocol", proto).
				Str("peer", addr).
				Dur("latency", latency).
				Msg("connect rpc")
			return resp, err
		}
	}
}

func errorsAsConnect(err error, target **connect.Error) bool {
	if err == nil {
		return false
	}
	var cerr *connect.Error
	if ok := errorsCastConnect(err, &cerr); ok {
		*target = cerr
		return true
	}
	return false
}

func errorsCastConnect(err error, target **connect.Error) bool {
	*target = nil
	type iface interface {
		As(interface{}) bool
	}
	e, ok := err.(iface)
	if ok {
		return e.As(target)
	}
	ct, ok := err.(*connect.Error)
	if ok {
		*target = ct
		return true
	}
	return false
}

func ConnectRecoverInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (resp connect.AnyResponse, err error) {
			defer func() {
				if r := recover(); r != nil {
					log.Error().
						Interface("panic", r).
						Str("procedure", req.Spec().Procedure).
						Msg("connect: panic recovered")
					err = apperror.ToConnect(apperror.ErrInternal)
				}
			}()
			return next(ctx, req)
		}
	}
}

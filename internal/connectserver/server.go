package connectserver

import (
	"context"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/grpcreflect"
	"github.com/rs/zerolog/log"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	apiv1connect "github.com/xuanphongtran/gogo-dl/gen/api/v1/apiv1connect"
	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/internal/config"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/internal/user"
)

type Server struct {
	httpServer *http.Server
}

func New(
	cfg *config.Config,
	userH *user.ConnectHandler,
	chatH *chat.ConnectHandler,
) *Server {
	reflector := grpcreflect.NewStaticReflector(
		"api.v1.UserService",
		"api.v1.ChatService",
	)

	interceptors := connect.WithOptions(
		connect.WithInterceptors(
			middleware.ConnectRecoverInterceptor(),
			middleware.ConnectLoggerInterceptor(),
			middleware.ConnectAuthInterceptor(cfg),
		),
	)

	mux := http.NewServeMux()

	path, handler := apiv1connect.NewUserServiceHandler(userH, interceptors)
	mux.Handle(path, handler)

	path, handler = apiv1connect.NewChatServiceHandler(chatH, interceptors)
	mux.Handle(path, handler)

	mux.Handle(grpcreflect.NewHandlerV1(reflector))
	mux.Handle(grpcreflect.NewHandlerV1Alpha(reflector))

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","time":"` + time.Now().UTC().Format(time.RFC3339) + `"}`))
	})

	h2s := &http2.Server{
		MaxConcurrentStreams: 256,
	}
	finalHandler := h2c.NewHandler(mux, h2s)

	return &Server{
		httpServer: &http.Server{
			Addr:         cfg.Addr(),
			Handler:      finalHandler,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
	}
}

func (s *Server) Start() error {
	log.Info().
		Str("addr", s.httpServer.Addr).
		Msg("connect: starting (grpc + grpc_web + connect_json on same port)")
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	log.Info().Msg("connect: server shutting down")
	done := make(chan struct{})
	go func() {
		s.httpServer.Shutdown(ctx)
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		s.httpServer.Close()
		return ctx.Err()
	}
}

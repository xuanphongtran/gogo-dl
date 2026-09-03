package user

import (
	"context"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/xuanphongtran/gogo-dl/gen/api/v1"
	apiv1connect "github.com/xuanphongtran/gogo-dl/gen/api/v1/apiv1connect"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

type ConnectHandler struct {
	apiv1connect.UnimplementedUserServiceHandler
	svc *Service
}

func NewConnectHandler(svc *Service) *ConnectHandler {
	return &ConnectHandler{svc: svc}
}

func (h *ConnectHandler) Register(
	ctx context.Context,
	req *connect.Request[apiv1.RegisterRequest],
) (*connect.Response[apiv1.TokenPair], error) {
	domainReq := &RegisterRequest{
		Username: req.Msg.Username,
		Email:    req.Msg.Email,
		Password: req.Msg.Password,
	}
	pair, err := h.svc.Register(ctx, domainReq)
	if err != nil {
		return nil, apperror.ToConnect(err)
	}
	return connect.NewResponse(toProtoTokenPair(pair)), nil
}

func (h *ConnectHandler) Login(
	ctx context.Context,
	req *connect.Request[apiv1.LoginRequest],
) (*connect.Response[apiv1.TokenPair], error) {
	domainReq := &LoginRequest{
		Email:    req.Msg.Email,
		Password: req.Msg.Password,
	}
	pair, err := h.svc.Login(ctx, domainReq)
	if err != nil {
		return nil, apperror.ToConnect(err)
	}
	return connect.NewResponse(toProtoTokenPair(pair)), nil
}

func (h *ConnectHandler) RefreshTokens(
	ctx context.Context,
	req *connect.Request[apiv1.RefreshTokensRequest],
) (*connect.Response[apiv1.TokenPair], error) {
	pair, err := h.svc.RefreshTokens(ctx, req.Msg.RefreshToken)
	if err != nil {
		return nil, apperror.ToConnect(err)
	}
	return connect.NewResponse(toProtoTokenPair(pair)), nil
}

func (h *ConnectHandler) GetMe(
	ctx context.Context,
	_ *connect.Request[emptypb.Empty],
) (*connect.Response[apiv1.ProfileResponse], error) {
	userID := middleware.MustGetUserIDConnect(ctx)
	profile, err := h.svc.GetProfile(ctx, userID)
	if err != nil {
		return nil, apperror.ToConnect(err)
	}
	return connect.NewResponse(toProtoProfile(profile)), nil
}

func (h *ConnectHandler) UpdateMe(
	ctx context.Context,
	req *connect.Request[apiv1.UpdateProfileRequest],
) (*connect.Response[apiv1.ProfileResponse], error) {
	userID := middleware.MustGetUserIDConnect(ctx)
	domainReq := &UpdateProfileRequest{}
	if req.Msg.Username != nil {
		domainReq.Username = *req.Msg.Username
	}
	if req.Msg.AvatarUrl != nil {
		domainReq.AvatarURL = *req.Msg.AvatarUrl
	}
	profile, err := h.svc.UpdateProfile(ctx, userID, domainReq)
	if err != nil {
		return nil, apperror.ToConnect(err)
	}
	return connect.NewResponse(toProtoProfile(profile)), nil
}

func (h *ConnectHandler) DeleteMe(
	ctx context.Context,
	_ *connect.Request[emptypb.Empty],
) (*connect.Response[emptypb.Empty], error) {
	userID := middleware.MustGetUserIDConnect(ctx)
	if err := h.svc.DeleteAccount(ctx, userID, userID); err != nil {
		return nil, apperror.ToConnect(err)
	}
	return connect.NewResponse(&emptypb.Empty{}), nil
}

func toProtoTokenPair(p *middleware.TokenPair) *apiv1.TokenPair {
	return &apiv1.TokenPair{
		AccessToken:  p.AccessToken,
		RefreshToken: p.RefreshToken,
		ExpiresAt:    p.ExpiresAt,
	}
}

func toProtoProfile(p *ProfileResponse) *apiv1.ProfileResponse {
	return &apiv1.ProfileResponse{
		Id:        p.ID,
		Username:  p.Username,
		Email:     p.Email,
		AvatarUrl: p.AvatarURL,
		CreatedAt: timestamppb.New(p.CreatedAt),
	}
}

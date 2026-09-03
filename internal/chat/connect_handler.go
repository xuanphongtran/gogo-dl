package chat

import (
	"context"
	"fmt"
	"strconv"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/xuanphongtran/gogo-dl/gen/api/v1"
	apiv1connect "github.com/xuanphongtran/gogo-dl/gen/api/v1/apiv1connect"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

type ConnectHandler struct {
	apiv1connect.UnimplementedChatServiceHandler
	svc *Service
	hub *ws.Hub
}

func NewConnectHandler(svc *Service, hub *ws.Hub) *ConnectHandler {
	return &ConnectHandler{svc: svc, hub: hub}
}

func (h *ConnectHandler) CreateRoom(
	ctx context.Context,
	req *connect.Request[apiv1.CreateRoomRequest],
) (*connect.Response[apiv1.Room], error) {
	userID := middleware.MustGetUserIDConnect(ctx)
	domainReq := &CreateRoomRequest{Name: req.Msg.Name}
	room, err := h.svc.CreateRoom(ctx, userID, domainReq)
	if err != nil {
		return nil, apperror.ToConnect(err)
	}
	return connect.NewResponse(toProtoRoom(room)), nil
}

func (h *ConnectHandler) ListRooms(
	ctx context.Context,
	_ *connect.Request[emptypb.Empty],
) (*connect.Response[apiv1.ListRoomsResponse], error) {
	rooms, err := h.svc.ListRooms(ctx)
	if err != nil {
		return nil, apperror.ToConnect(err)
	}
	protoRooms := make([]*apiv1.Room, len(rooms))
	for i, r := range rooms {
		protoRooms[i] = toProtoRoom(r)
	}
	return connect.NewResponse(&apiv1.ListRoomsResponse{Rooms: protoRooms}), nil
}

func (h *ConnectHandler) GetRoom(
	ctx context.Context,
	req *connect.Request[apiv1.GetRoomRequest],
) (*connect.Response[apiv1.Room], error) {
	room, err := h.svc.GetRoom(ctx, req.Msg.Id)
	if err != nil {
		return nil, apperror.ToConnect(err)
	}
	return connect.NewResponse(toProtoRoom(room)), nil
}

func (h *ConnectHandler) JoinRoom(
	ctx context.Context,
	req *connect.Request[apiv1.JoinRoomRequest],
) (*connect.Response[emptypb.Empty], error) {
	userID := middleware.MustGetUserIDConnect(ctx)
	if err := h.svc.JoinRoom(ctx, req.Msg.RoomId, userID); err != nil {
		return nil, apperror.ToConnect(err)
	}
	return connect.NewResponse(&emptypb.Empty{}), nil
}

func (h *ConnectHandler) SendMessage(
	ctx context.Context,
	req *connect.Request[apiv1.SendMessageRequest],
) (*connect.Response[apiv1.Message], error) {
	userID := middleware.MustGetUserIDConnect(ctx)
	username := fmt.Sprintf("user_%d", userID)
	domainReq := &SendMessageRequest{Content: req.Msg.Content}
	msg, err := h.svc.SendMessage(ctx, userID, req.Msg.RoomId, domainReq, username)
	if err != nil {
		return nil, apperror.ToConnect(err)
	}
	return connect.NewResponse(toProtoMessage(msg)), nil
}

func (h *ConnectHandler) ListMessages(
	ctx context.Context,
	req *connect.Request[apiv1.ListMessagesRequest],
) (*connect.Response[apiv1.ListMessagesResponse], error) {
	roomID := req.Msg.RoomId
	q := &ListMessagesQuery{
		Limit:  int(req.Msg.Limit),
		Before: req.Msg.Before,
	}
	msgs, err := h.svc.ListMessages(ctx, roomID, q)
	if err != nil {
		return nil, apperror.ToConnect(err)
	}
	protoMsgs := make([]*apiv1.Message, len(msgs))
	for i, m := range msgs {
		protoMsgs[i] = toProtoMessage(m)
	}
	return connect.NewResponse(&apiv1.ListMessagesResponse{Messages: protoMsgs}), nil
}

func (h *ConnectHandler) StreamMessages(
	ctx context.Context,
	req *connect.Request[apiv1.StreamMessagesRequest],
	stream *connect.ServerStream[apiv1.Message],
) error {
	userID := middleware.MustGetUserIDConnect(ctx)
	roomID := req.Msg.RoomId
	if err := h.svc.JoinRoom(ctx, roomID, userID); err != nil {
		return apperror.ToConnect(err)
	}
	wsRoomID := strconv.FormatInt(roomID, 10)
	ch := make(chan *apiv1.Message, 256)
	clientID := fmt.Sprintf("stream-%d-%s", userID, wsRoomID)
	ws.RegisterStreamSubscriber(clientID, wsRoomID, func(m ws.Message) {
		if m.Type != ws.EventMessage {
			return
		}
		payload, ok := m.Payload.(map[string]interface{})
		if !ok {
			return
		}
		msg, err := payloadToProtoMessage(roomID, payload)
		if err != nil {
			return
		}
		select {
		case ch <- msg:
		default:
		}
	})
	defer func() {
		ws.UnregisterStreamSubscriber(clientID, wsRoomID)
		close(ch)
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(msg); err != nil {
				return nil
			}
		}
	}
}

func toProtoRoom(r *Room) *apiv1.Room {
	return &apiv1.Room{
		Id:        r.ID,
		Name:      r.Name,
		CreatedBy: r.CreatedBy,
		CreatedAt: timestamppb.New(r.CreatedAt),
	}
}

func toProtoMessage(m *Message) *apiv1.Message {
	return &apiv1.Message{
		Id:        m.ID,
		RoomId:    m.RoomID,
		UserId:    m.UserID,
		Username:  m.Username,
		Content:   m.Content,
		CreatedAt: timestamppb.New(m.CreatedAt),
	}
}

func payloadToProtoMessage(roomID int64, payload map[string]interface{}) (*apiv1.Message, error) {
	msg := &apiv1.Message{RoomId: roomID}
	if id, ok := payload["id"].(int64); ok {
		msg.Id = id
	}
	if uid, ok := payload["user_id"].(int64); ok {
		msg.UserId = uid
	}
	if u, ok := payload["username"].(string); ok {
		msg.Username = u
	}
	if c, ok := payload["content"].(string); ok {
		msg.Content = c
	}
	if t, ok := payload["created_at"]; ok {
		_ = t
	}
	return msg, nil
}

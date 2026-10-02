package chat

import (
	"context"
	"testing"

	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestSendMessageTracePreservesParentWithoutCapturingData(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous); _ = provider.Shutdown(context.Background()) })
	ctx, root := provider.Tracer("test").Start(context.Background(), "request")
	repo, mock := newRepositoryTest(t)
	expectRoomByID(mock, &Room{ID: 10, Visibility: RoomVisibilityPublic})
	expectMembershipCount(mock, 10, 7, true)
	expectCreatedMessage(mock, 10, 7, "private-message")
	if _, err := NewService(repo, ws.New()).SendMessage(ctx, 7, 10, &SendMessageRequest{Content: "private-message"}, "private-user"); err != nil {
		t.Fatal(err)
	}
	root.End()
	spans := map[string]sdktrace.ReadOnlySpan{}
	for _, span := range recorder.Ended() {
		spans[span.Name()] = span
		if len(span.Attributes()) != 0 || len(span.Events()) != 0 {
			t.Fatal("domain span captured data")
		}
	}
	service := spans["chat.service.SendMessage"]
	if service == nil || service.Parent().SpanID() != root.SpanContext().SpanID() {
		t.Fatal("service lost request parent")
	}
	for _, name := range []string{"chat.repository.GetRoomByID", "chat.repository.IsMember", "chat.repository.CreateMessage"} {
		span := spans[name]
		if span == nil || span.Parent().SpanID() != service.SpanContext().SpanID() {
			t.Fatalf("%s lost service parent", name)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

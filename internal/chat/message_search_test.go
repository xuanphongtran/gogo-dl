package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/config"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

type searchRepository struct {
	Repository
	visibility RoomVisibility
	member     *RoomMember
	messages   []*Message
	failure    error
	calls      int
	query      string
	limit      int
	before     int64
	ctx        context.Context
}

func (r *searchRepository) SearchMessages(ctx context.Context, roomID, userID int64, query string, limit int, before int64, policy ReadStatePolicy) ([]*Message, error) {
	r.calls++
	r.ctx, r.query, r.limit, r.before = ctx, query, limit, before
	if roomID != 10 || userID != 7 {
		return nil, errors.New("incorrect trusted identity or room")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := policy(r.visibility, r.member); err != nil {
		return nil, err
	}
	return r.messages, r.failure
}

func newSearchRepository() *searchRepository {
	return &searchRepository{visibility: RoomVisibilityPublic, member: &RoomMember{RoomID: 10, UserID: 7}}
}

func TestSearchMessagesValidation(t *testing.T) {
	zero, negative, high := 0, -1, 101
	zeroID, negativeID := int64(0), int64(-1)
	for _, req := range []*SearchMessagesQuery{
		nil, {}, {Query: "  "}, {Query: "\xff"}, {Query: "a\x00b"},
		{Query: strings.Repeat("a", 257)}, {Query: strings.Repeat("ế", 86)},
		{Query: "hello", Limit: &zero}, {Query: "hello", Limit: &negative}, {Query: "hello", Limit: &high},
		{Query: "hello", Before: &zeroID}, {Query: "hello", Before: &negativeID},
	} {
		repo := newSearchRepository()
		if _, err := NewService(repo, nil).SearchMessages(context.Background(), 7, 10, req); !errors.Is(err, apperror.ErrInvalidRequest) || repo.calls != 0 {
			t.Fatalf("invalid request reached persistence: err=%v calls=%d", err, repo.calls)
		}
	}
	for _, ids := range [][2]int64{{0, 10}, {7, 0}, {-1, 10}} {
		repo := newSearchRepository()
		_, err := NewService(repo, nil).SearchMessages(context.Background(), ids[0], ids[1], &SearchMessagesQuery{Query: "hello"})
		if !errors.Is(err, apperror.ErrInvalidRequest) || repo.calls != 0 {
			t.Fatal("invalid identity accepted")
		}
	}
}

func TestSearchMessagesPaginationAndContext(t *testing.T) {
	limit, before := 2, int64(100)
	for _, count := range []int{0, 1, 2, 3} {
		repo := newSearchRepository()
		for i := 0; i < count; i++ {
			repo.messages = append(repo.messages, &Message{ID: int64(90 - i)})
		}
		ctx, cancel := context.WithCancel(context.Background())
		result, err := NewService(repo, nil).SearchMessages(ctx, 7, 10, &SearchMessagesQuery{Query: "  chào bạn  ", Limit: &limit, Before: &before})
		cancel()
		if err != nil || result.Messages == nil || repo.query != "chào bạn" || repo.limit != 3 || repo.before != 100 || repo.ctx.Err() != context.Canceled {
			t.Fatalf("page/context mismatch: result=%+v err=%v", result, err)
		}
		if count > limit {
			if len(result.Messages) != 2 || result.NextBefore == nil || *result.NextBefore != 89 {
				t.Fatalf("lookahead cursor = %+v", result)
			}
		} else if len(result.Messages) != count || result.NextBefore != nil {
			t.Fatalf("terminal page = %+v", result)
		}
	}
	repo := newSearchRepository()
	_, err := NewService(repo, nil).SearchMessages(context.Background(), 7, 10, &SearchMessagesQuery{Query: strings.Repeat("a", 256)})
	if err != nil || repo.limit != 21 || repo.before != 0 {
		t.Fatalf("default bounds: %v", err)
	}
}

func TestSearchMessagesAuthorizationAndFailures(t *testing.T) {
	failure := errors.New("private database detail")
	for _, tc := range []struct {
		visibility RoomVisibility
		member     bool
		failure    error
		want       error
	}{
		{RoomVisibilityPublic, false, nil, apperror.ErrForbidden},
		{RoomVisibilityPrivate, false, nil, apperror.ErrNotFound},
		{RoomVisibilityPrivate, true, nil, nil},
		{RoomVisibilityPublic, true, failure, failure},
	} {
		repo := newSearchRepository()
		repo.visibility, repo.failure = tc.visibility, tc.failure
		if !tc.member {
			repo.member = nil
		}
		_, err := NewService(repo, nil).SearchMessages(context.Background(), 7, 10, &SearchMessagesQuery{Query: "hello"})
		if !errors.Is(err, tc.want) {
			t.Fatalf("error=%v want=%v", err, tc.want)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewService(newSearchRepository(), nil).SearchMessages(ctx, 7, 10, &SearchMessagesQuery{Query: "hello"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestSearchMessagesHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{JWTAccessSecret: "search-test-access", JWTRefreshSecret: "search-test-refresh", JWTAccessTTL: time.Hour, JWTRefreshTTL: time.Hour}
	tokens, err := middleware.GenerateTokenPair(cfg, 7)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, target string
		status       int
		auth         bool
		setup        func(*searchRepository)
	}{
		{"success", "/api/v1/rooms/10/messages/search?q=hello", 200, true, nil},
		{"missing auth", "/api/v1/rooms/10/messages/search?q=hello", 401, false, nil},
		{"bad room", "/api/v1/rooms/0/messages/search?q=hello", 400, true, nil},
		{"missing query", "/api/v1/rooms/10/messages/search", 400, true, nil},
		{"blank", "/api/v1/rooms/10/messages/search?q=%20", 400, true, nil},
		{"UTF8", "/api/v1/rooms/10/messages/search?q=%ff", 400, true, nil},
		{"byte bound", "/api/v1/rooms/10/messages/search?q=" + url.QueryEscape(strings.Repeat("ế", 86)), 400, true, nil},
		{"zero limit", "/api/v1/rooms/10/messages/search?q=hello&limit=0", 400, true, nil},
		{"high limit", "/api/v1/rooms/10/messages/search?q=hello&limit=101", 400, true, nil},
		{"bad limit", "/api/v1/rooms/10/messages/search?q=hello&limit=bad", 400, true, nil},
		{"zero cursor", "/api/v1/rooms/10/messages/search?q=hello&before=0", 400, true, nil},
		{"negative cursor", "/api/v1/rooms/10/messages/search?q=hello&before=-1", 400, true, nil},
		{"overflow cursor", "/api/v1/rooms/10/messages/search?q=hello&before=9223372036854775808", 400, true, nil},
		{"forbidden", "/api/v1/rooms/10/messages/search?q=hello", 403, true, func(r *searchRepository) { r.member = nil }},
		{"private hidden", "/api/v1/rooms/10/messages/search?q=hello", 404, true, func(r *searchRepository) { r.member = nil; r.visibility = RoomVisibilityPrivate }},
		{"missing room", "/api/v1/rooms/10/messages/search?q=hello", 404, true, func(r *searchRepository) { r.failure = apperror.ErrNotFound }},
		{"unsearchable", "/api/v1/rooms/10/messages/search?q=%21", 400, true, func(r *searchRepository) { r.failure = apperror.ErrInvalidRequest }},
		{"database failure", "/api/v1/rooms/10/messages/search?q=hello", 500, true, func(r *searchRepository) { r.failure = errors.New("private database detail") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newSearchRepository()
			if tc.setup != nil {
				tc.setup(repo)
			}
			router := gin.New()
			private := router.Group("/api/v1", middleware.Auth(cfg))
			NewHandler(NewService(repo, nil), nil).RegisterRoutes(private)
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			if tc.auth {
				req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
			}
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			if res.Code != tc.status || strings.Contains(res.Body.String(), "private database detail") {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			if tc.status == 200 {
				var body SearchMessagesResponse
				if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || body.Messages == nil || body.NextBefore != nil {
					t.Fatalf("empty page=%s error=%v", res.Body.String(), err)
				}
			}
		})
	}
}

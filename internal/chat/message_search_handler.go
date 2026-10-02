package chat

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// SearchMessages returns live matches for a current room member.
// @Summary      Search room messages
// @Description  Plain-text AND search using PostgreSQL simple configuration. Excludes deleted messages; ordered by descending ID. Restart pagination when changing q. No stemming or accent folding guarantee.
// @Tags         messages
// @Produce      json
// @Security     BearerAuth
// @Param        id      path   int64   true   "Room ID" minimum(1)
// @Param        q       query  string  true   "Trimmed searchable UTF-8 text, 1–256 bytes"
// @Param        limit   query  int     false  "Page size" default(20) minimum(1) maximum(100)
// @Param        before  query  int64   false  "Exclusive message ID cursor" minimum(1)
// @Success      200  {object} SearchMessagesResponse
// @Failure      400  {object} apperror.AppError
// @Failure      401  {object} apperror.AppError
// @Failure      403  {object} apperror.AppError
// @Failure      404  {object} apperror.AppError
// @Failure      500  {object} apperror.AppError
// @Router       /api/v1/rooms/{id}/messages/search [get]
func (h *Handler) SearchMessages(c *gin.Context) {
	roomID, err := parsePathID(c, "id")
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	var req SearchMessagesQuery
	if err := c.ShouldBindQuery(&req); err != nil {
		apperror.Respond(c, middleware.BindingError(err))
		return
	}
	result, err := h.svc.SearchMessages(c.Request.Context(), middleware.MustGetUserID(c), roomID, &req)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

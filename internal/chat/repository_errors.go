package chat

import (
	"errors"

	"github.com/lib/pq"
)

func isPostgresConstraintCode(err error, code pq.ErrorCode) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == code
}

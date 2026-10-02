package database

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// golang-migrate's PostgreSQL advisory-lock and version-table queries use
// Background internally. Bound them on this owned session, without introducing
// another lock. Restore the original setting before returning it to the pool.
func boundMigrationSession(ctx context.Context, conn *sql.Conn) (func() error, error) {
	if err := conn.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("database: migration ping: %w", err)
	}
	var original string
	if err := conn.QueryRowContext(ctx, "SHOW statement_timeout").Scan(&original); err != nil {
		return nil, fmt.Errorf("database: read migration session timeout: %w", err)
	}
	limit := 5 * time.Second
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < limit {
		limit = time.Until(deadline)
	}
	if limit <= 0 {
		return nil, context.DeadlineExceeded
	}
	milliseconds := (limit + time.Millisecond - 1) / time.Millisecond
	if _, err := conn.ExecContext(ctx, "SELECT set_config('statement_timeout', $1, false)", strconv.FormatInt(int64(milliseconds), 10)+"ms"); err != nil {
		// A cancelled SET may have reached PostgreSQL even if its reply was lost.
		discardErr := conn.Raw(func(any) error { return sqldriver.ErrBadConn })
		if errors.Is(discardErr, sqldriver.ErrBadConn) {
			discardErr = nil
		}
		return nil, errors.Join(fmt.Errorf("database: set migration session timeout: %w", err), discardErr)
	}
	return func() error {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		if _, err := conn.ExecContext(cleanup, "SELECT set_config('statement_timeout', $1, false)", original); err != nil {
			// Never return a session with an unknown timeout to business queries.
			discardErr := conn.Raw(func(any) error { return sqldriver.ErrBadConn })
			if errors.Is(discardErr, sqldriver.ErrBadConn) {
				discardErr = nil
			}
			return errors.Join(fmt.Errorf("database: restore migration session timeout: %w", err), discardErr)
		}
		return nil
	}, nil
}

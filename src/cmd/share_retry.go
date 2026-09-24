package cmd

import (
	"context"
	"time"

	"github.com/bresilla/drop/src/pkg/arch/share"
	"github.com/bresilla/drop/src/pkg/wire"
)

const (
	shareAttempts   = 3
	shareRetryDelay = 100 * time.Millisecond
)

type openTransfer func(context.Context) (*wire.Conn, func(), error)

func retryTransfer(ctx context.Context, transfer *share.Transfer, progress func(string, int64, int64), open openTransfer) error {
	var last error
	for attempt := range shareAttempts {
		conn, close, err := open(ctx)
		if err != nil {
			return err
		}
		err = transfer.Send(conn, progress)
		close()
		if err == nil || !share.Unconfirmed(err) || attempt == shareAttempts-1 {
			return err
		}
		last = err

		timer := time.NewTimer(shareRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return last
}

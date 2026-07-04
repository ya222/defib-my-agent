package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// defaultFollowInterval is how often a follower re-checks the file for
// appended data once it has caught up.
const defaultFollowInterval = 200 * time.Millisecond

// Follow opens path and returns a reader that streams its contents and then
// keeps returning data as the file grows (tail -f), so `task.logs --follow`
// can stream an in-progress attempt log. When ctx is canceled the reader
// finishes the bytes already read and reports io.EOF. Close releases the
// file; the follower must be closed by the caller.
func Follow(ctx context.Context, path string) (io.ReadCloser, error) {
	return follow(ctx, path, defaultFollowInterval)
}

func follow(ctx context.Context, path string, interval time.Duration) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("follow %s: %w", path, err)
	}
	return &follower{ctx: ctx, f: f, interval: interval}, nil
}

type follower struct {
	ctx      context.Context
	f        *os.File
	interval time.Duration
}

// Read returns available bytes immediately; at end-of-file it waits for the
// writer to append more instead of reporting EOF, using a timer rather than
// a sleep loop so cancellation is prompt.
func (fl *follower) Read(p []byte) (int, error) {
	timer := time.NewTimer(fl.interval)
	defer timer.Stop()

	for {
		n, err := fl.f.Read(p)
		if n > 0 {
			// Data and a concurrent EOF can arrive together; surface the
			// data now, the next Read revisits the file end.
			return n, nil
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, fmt.Errorf("follow %s: %w", fl.f.Name(), err)
		}

		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(fl.interval)
		select {
		case <-fl.ctx.Done():
			return 0, io.EOF
		case <-timer.C:
		}
	}
}

func (fl *follower) Close() error {
	if err := fl.f.Close(); err != nil {
		return fmt.Errorf("close follower: %w", err)
	}
	return nil
}

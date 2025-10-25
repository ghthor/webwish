package ctxhelp

import "context"

func Join(ctx1, ctx2 context.Context) (context.Context, context.CancelCauseFunc) {
	ctx, cancel := context.WithCancelCause(context.Background())

	go func() {
		select {
		case <-ctx1.Done():
			cancel(ctx1.Err())
		case <-ctx2.Done():
			cancel(ctx2.Err())
		case <-ctx.Done(): // if the joined context itself is canceled
		}
	}()

	return ctx, cancel
}

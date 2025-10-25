package webwish

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/ghthor/gotty/v2/server"
	"github.com/ghthor/gotty/v2/utils"
	"golang.org/x/sync/errgroup"
)

func RunSSH(ctx context.Context, grp *errgroup.Group, cancel context.CancelCauseFunc, l net.Listener, s *ssh.Server) error {
	grp.Go(func() error {
		if err := s.Serve(l); err != nil && !errors.Is(err, ssh.ErrServerClosed) {
			cancel(err)
			return err
		}
		return nil
	})

	return nil
}

func ShutdownSSH(s *ssh.Server, timeout time.Duration) error {
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil && !errors.Is(err, ssh.ErrServerClosed) {
		if errors.Is(err, context.DeadlineExceeded) {
			return s.Close()
		}
		return err
	}
	return nil
}

func RunHTTP(ctx context.Context, grp *errgroup.Group, cancel context.CancelCauseFunc, l net.Listener, fact server.Factory) error {
	var (
		err        error
		appOptions = &server.Options{}
	)

	if err = utils.ApplyDefaultValues(appOptions); err != nil {
		return fmt.Errorf("gotty default options failure: %w", err)
	}
	appOptions.Preferences = &server.HtermPrefernces{}
	if err = utils.ApplyDefaultValues(appOptions.Preferences); err != nil {
		return fmt.Errorf("gotty default hterm preferences failure: %w", err)
	}
	appOptions.Preferences.EnableWebGL = true
	appOptions.PermitWrite = true

	if err = appOptions.Validate(); err != nil {
		return fmt.Errorf("gotty options validation failure: %w", err)
	}

	var gottySrv *server.Server
	gottySrv, err = server.New(fact, appOptions)
	if err != nil {
		return fmt.Errorf("error creating gotty server: %w", err)
	}

	grp.Go(func() error {
		if serr := gottySrv.Run(ctx, server.WithListener(l)); serr != nil && !errors.Is(serr, context.Canceled) {
			cancel(serr)
			return serr
		}
		return nil
	})

	return nil
}

package tshelper

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/charmbracelet/log"
	"tailscale.com/client/local"
	"tailscale.com/tsnet"
)

type Listeners struct {
	ts *tsnet.Server

	Ssh, Http net.Listener

	Client *local.Client
}

func NewListeners(hostname string, sshPort, httpPort int) (Listeners, error) {
	l := Listeners{}
	l.ts = new(tsnet.Server)
	l.ts.Hostname = hostname

	var err error
	l.Ssh, err = l.ts.Listen("tcp", net.JoinHostPort("", fmt.Sprint(sshPort)))
	if err != nil {
		return l, errors.Join(
			fmt.Errorf("failed to start ssh listener: %w", err),
			l.Close(),
		)
	}

	l.Http, err = l.ts.Listen("tcp", net.JoinHostPort("", fmt.Sprint(httpPort)))
	if err != nil {
		return l, errors.Join(
			fmt.Errorf("failed to start http listener: %w", err),
			l.Close(),
		)
	}

	l.Client, err = l.ts.LocalClient()
	if err != nil {
		return l, errors.Join(
			fmt.Errorf("failed to create tsnet LocalClient(): %w", err),
			l.Close(),
		)
	}

	return l, nil
}

func (l Listeners) WaitForTailscaleIP(ctx context.Context) (v4, v6 netip.Addr, err error) {
	var (
		t    = time.NewTicker(time.Second)
		done = ctx.Done()
	)
	defer t.Stop()

	for {
		select {
		case <-done:
			return v4, v6, ctx.Err()

		case <-t.C:
			v4, v6 = l.ts.TailscaleIPs()
			if v4.IsValid() {
				return v4, v6, nil
			}
			log.Info("Waiting for tailscale IP")
		}
	}
}

func (l Listeners) Close() error {
	errs := make([]error, 0, 3)
	if l.Ssh != nil {
		errs = append(errs, l.Ssh.Close())
	}
	if l.Http != nil {
		errs = append(errs, l.Http.Close())
	}
	if l.ts != nil {
		errs = append(errs, l.ts.Close())
	}

	return errors.Join(errs...)
}

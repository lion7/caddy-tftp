package internal

import (
	"fmt"
	"go.uber.org/zap"
	"net"

	"github.com/caddyserver/caddy/v2"
	"go.universe.tf/netboot/tftp"
	"golang.org/x/sync/errgroup"
)

func init() {
	caddy.RegisterModule(TFTP{})
}

type caddySshServerCtxKey string

const CtxServerName caddySshServerCtxKey = "CtxServerName"

// TFTP is an example; put your own type here.
type TFTP struct {
	// The set of ssh servers keyed by custom names
	Servers  map[string]*Server `json:"servers,omitempty"`
	servers  []tftpServer
	ctx      caddy.Context
	errGroup *errgroup.Group
}

type Server struct {
	// Socket addresses to which to bind listeners.
	// Accepts network addresses that may include port ranges.
	// Listener addresses must be unique; they cannot be repeated across all defined servers.
	// UDP is the only acceptable network.
	Listen string `json:"listen,omitempty"`

	// The path to the root of the site.
	// Default is current working directory.
	// This should be a trusted value.
	Root string `json:"root,omitempty"`
}

type tftpServer struct {
	*tftp.Server
	name string
	addr caddy.NetworkAddress
	log  *zap.Logger
}

// CaddyModule returns the Caddy module information.
func (TFTP) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "tftp",
		New: func() caddy.Module { return new(TFTP) },
	}
}

func (app *TFTP) Provision(ctx caddy.Context) error {
	app.ctx = ctx
	for name, srv := range app.Servers {
		addr, err := caddy.ParseNetworkAddressWithDefaults(srv.Listen, "udp", 69)
		if err != nil {
			return err
		}
		if addr.Network != "udp" {
			return fmt.Errorf("only 'udp' is supported in the listener addr")
		}
		for portOffset := uint(0); portOffset < addr.PortRangeSize(); portOffset++ {
			handler, err := tftp.FilesystemHandler(srv.Root)
			if err != nil {
				return err
			}
			tftpServer := tftpServer{
				Server: &tftp.Server{
					Handler: handler,
				},
				name: name,
				addr: addr,
				log:  ctx.Logger().Named(name),
			}
			app.servers = append(app.servers, tftpServer)
		}
	}
	return nil
}

// Start starts the TFTP app.
func (app *TFTP) Start() error {
	app.errGroup = &errgroup.Group{}
	for _, srv := range app.servers {
		ln, err := srv.addr.Listen(app.ctx, 0, net.ListenConfig{})
		if err != nil {
			return fmt.Errorf("tftp: failed to listen on %s: %v", srv.addr, err)
		}
		l, ok := ln.(net.PacketConn)
		if !ok {
			return fmt.Errorf("tftp: failed to listen on %s: %v", srv.addr, err)
		}
		app.errGroup.Go(func() error {
			srv.log.Info(fmt.Sprintf("listening on %s", srv.addr))
			return srv.Serve(l)
		})
	}
	return nil
}

// Stop stops the TFTP app.
func (app *TFTP) Stop() error {
	return app.errGroup.Wait()
}

// Interface guards
var (
	_ caddy.Provisioner = (*TFTP)(nil)
	_ caddy.App         = (*TFTP)(nil)
)

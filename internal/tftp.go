package internal

import (
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/pin/tftp/v3"
	"go.uber.org/zap"
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
	servers  []*tftpServer
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

	// How long to allow a read or write from a client.
	// Duration can be an integer or a string.
	// An integer is interpreted as nanoseconds.
	// If a string, it is a Go time.Duration value such as 300ms, 1.5h, or 2h45m;
	// valid units are ns, us/µs, ms, s, m, h, and d.
	Timeout time.Duration `json:"timeout,omitempty"`
}

type tftpServer struct {
	*tftp.Server
	name string
	root string
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
		root := srv.Root
		if root == "" {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			root = wd
		}

		addr, err := caddy.ParseNetworkAddressWithDefaults(srv.Listen, "udp", 69)
		if err != nil {
			return err
		}
		if addr.Network != "udp" {
			return fmt.Errorf("only 'udp' is supported in the listener addr")
		}

		tftpServer := &tftpServer{
			name: name,
			root: root,
			addr: addr,
			log:  ctx.Logger().Named(name),
		}
		tftpServer.Server = tftp.NewServer(tftpServer.readHandler, tftpServer.writeHandler)
		app.servers = append(app.servers, tftpServer)
	}
	return nil
}

// Start starts the TFTP app.
func (app *TFTP) Start() error {
	app.errGroup = &errgroup.Group{}
	for _, s := range app.servers {
		ln, err := s.addr.Listen(app.ctx, 0, net.ListenConfig{})
		if err != nil {
			return fmt.Errorf("tftp: failed to listen on %s: %v", s.addr, err)
		}
		l, ok := ln.(net.PacketConn)
		if !ok {
			return fmt.Errorf("tftp: failed to listen on %s: %v", s.addr, err)
		}
		app.errGroup.Go(func() error {
			s.log.Info(fmt.Sprintf("listening on %s", s.addr))
			return s.Serve(l)
		})
	}
	return nil
}

// Stop stops the TFTP app.
func (app *TFTP) Stop() error {
	for _, s := range app.servers {
		s.Shutdown()
	}
	return app.errGroup.Wait()
}

// readHandler is called when client starts file download from server
func (s *tftpServer) readHandler(filename string, rf io.ReaderFrom) error {
	p := path.Join(s.root, filename)
	file, err := os.Open(p)
	if err != nil {
		s.log.Error(err.Error())
		return err
	}
	n, err := rf.ReadFrom(file)
	if err != nil {
		s.log.Error(err.Error())
		return err
	}
	s.log.Info(fmt.Sprintf("%d bytes sent", n))
	return nil
}

// writeHandler is called when client starts file upload to server
func (s *tftpServer) writeHandler(filename string, wt io.WriterTo) error {
	p := path.Join(s.root, filename)
	file, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		s.log.Error(err.Error())
		return err
	}
	n, err := wt.WriteTo(file)
	if err != nil {
		s.log.Error(err.Error())
		return err
	}
	s.log.Info(fmt.Sprintf("%d bytes received", n))
	return nil
}

// Interface guards
var (
	_ caddy.Provisioner = (*TFTP)(nil)
	_ caddy.App         = (*TFTP)(nil)
)

package internal

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/pin/tftp/v3"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

func init() {
	caddy.RegisterModule(TFTP{})
}

type TFTP struct {
	// The set of ssh servers keyed by custom names
	Servers map[string]*Server `json:"servers,omitempty"`

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

	// The maximum time to wait for a single network round-trip to succeed.
	// Default is 5 seconds.
	// Duration can be an integer or a string.
	// An integer is interpreted as nanoseconds.
	// If a string, it is a Go time.Duration value such as 300ms, 1.5h, or 2h45m;
	// valid units are ns, us/µs, ms, s, m, h, and d.
	Timeout caddy.Duration `json:"timeout,omitempty"`

	// Enables access logging.
	Logs bool `json:"logs,omitempty"`
}

type tftpServer struct {
	*tftp.Server
	name      string
	root      string
	addr      caddy.NetworkAddress
	log       *zap.Logger
	accessLog *zap.Logger
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
		root, err := filepath.Abs(srv.Root)
		if err != nil {
			return err
		}

		addr, err := caddy.ParseNetworkAddressWithDefaults(srv.Listen, "udp", 69)
		if err != nil {
			return err
		}
		if addr.Network != "udp" {
			return fmt.Errorf("only 'udp' is supported in the listener addr")
		}

		log := ctx.Logger().Named(name)
		s := &tftpServer{
			name:      name,
			root:      root,
			addr:      addr,
			log:       log,
			accessLog: log.Named("access"),
		}
		tftpServer := tftp.NewServer(s.readHandler, s.writeHandler)
		tftpServer.SetTimeout(time.Duration(srv.Timeout))
		s.Server = tftpServer

		app.servers = append(app.servers, s)
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
			s.log.Info(
				"server running",
				zap.String("name", s.name),
				zap.String("address", s.addr.String()),
				zap.String("root", s.root),
			)
			return s.Serve(l)
		})
	}
	return nil
}

// Stop stops the TFTP app.
func (app *TFTP) Stop() error {
	for _, s := range app.servers {
		s.Shutdown()
		s.log.Info(
			"server stopped",
			zap.String("name", s.name),
			zap.String("address", s.addr.String()),
			zap.String("root", s.root),
		)
	}
	return app.errGroup.Wait()
}

// readHandler is called when client starts file download from server
func (s *tftpServer) readHandler(filename string, rf io.ReaderFrom) error {
	var n int64
	if s.accessLog != nil {
		var remoteIP net.IP
		var remotePort int
		if ot, ok := rf.(tftp.OutgoingTransfer); ok {
			remoteIP = ot.RemoteAddr().IP
			remotePort = ot.RemoteAddr().Port
		}
		start := time.Now()
		defer func() {
			end := time.Now()
			d := end.Sub(start)
			s.accessLog.Info(
				"handled request",
				zap.String("remote_ip", remoteIP.String()),
				zap.Int("remote_port", remotePort),
				zap.String("method", "GET"),
				zap.String("uri", filename),
				zap.Int64("bytes_written", n),
				zap.String("duration", d.String()),
			)
		}()
	}

	p, err := s.safePath(filename)
	if err != nil {
		s.log.Error(err.Error(), zap.String("filename", filename))
		return err
	}
	file, err := os.Open(p)
	if err != nil {
		s.log.Error(err.Error(), zap.String("filename", filename))
		return err
	}
	n, err = rf.ReadFrom(file)
	if err != nil {
		s.log.Error(err.Error(), zap.String("filename", filename))
		return err
	}
	return nil
}

// writeHandler is called when client starts file upload to server
func (s *tftpServer) writeHandler(filename string, wt io.WriterTo) error {
	var n int64
	if s.accessLog != nil {
		var remoteIP net.IP
		var remotePort int
		if ot, ok := wt.(tftp.IncomingTransfer); ok {
			remoteIP = ot.RemoteAddr().IP
			remotePort = ot.RemoteAddr().Port
		}
		start := time.Now()
		defer func() {
			end := time.Now()
			d := end.Sub(start)
			s.accessLog.Info(
				"handled request",
				zap.String("remote_ip", remoteIP.String()),
				zap.Int("remote_port", remotePort),
				zap.String("method", "PUT"),
				zap.String("uri", filename),
				zap.Int64("bytes_read", n),
				zap.String("duration", d.String()),
			)
		}()
	}

	p, err := s.safePath(filename)
	if err != nil {
		s.log.Error(err.Error(), zap.String("filename", filename))
		return err
	}
	file, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		s.log.Error(err.Error(), zap.String("filename", filename))
		return err
	}
	n, err = wt.WriteTo(file)
	if err != nil {
		s.log.Error(err.Error(), zap.String("filename", filename))
		return err
	}
	return nil
}

func (s *tftpServer) safePath(filename string) (string, error) {
	p := filepath.Join(s.root, filename)
	c, err := filepath.Abs(p)
	s.log.Debug(
		"sanitized path join",
		zap.String("root", s.root),
		zap.String("filename", filename),
		zap.String("result", c),
	)
	if err != nil || !strings.HasPrefix(c, s.root) {
		return c, errors.New("unsafe or invalid filename specified")
	} else {
		return c, nil
	}
}

// Interface guards
var (
	_ caddy.Provisioner = (*TFTP)(nil)
	_ caddy.App         = (*TFTP)(nil)
)

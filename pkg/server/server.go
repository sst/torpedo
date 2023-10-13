package server

import (
	"log"
	"net"

	"github.com/gliderlabs/ssh"
)

type Server struct {
	server *ssh.Server
}

func NewServer(publicRaw []byte) (*Server, error) {
	pub, err := ssh.ParsePublicKey(publicRaw)
	if err != nil {
		return nil, err
	}
	result := &Server{}

	result.server = &ssh.Server{
		PublicKeyHandler: func(ctx ssh.Context, key ssh.PublicKey) bool {
			compare := ssh.KeysEqual(pub, key)
			log.Println("public key matches", compare)
			return compare
		},
		LocalPortForwardingCallback: ssh.LocalPortForwardingCallback(func(ctx ssh.Context, dhost string, dport uint32) bool {
			log.Println("forwarding for", dhost, dport)
			return true
		}),
		ConnCallback: func(ctx ssh.Context, conn net.Conn) net.Conn {
			log.Println("connection from", conn.RemoteAddr())
			return conn
		},
		Addr: ":2222",
		Handler: ssh.Handler(func(s ssh.Session) {
			select {}
		}),
		ChannelHandlers: map[string]ssh.ChannelHandler{
			"session":      ssh.DefaultSessionHandler,
			"direct-tcpip": ssh.DirectTCPIPHandler,
		},
	}
	return result, nil
}

func (s *Server) Start() error {
	log.Println("starting server")
	err := s.server.ListenAndServe()
	if err != nil {
		return err
	}
	return nil
}

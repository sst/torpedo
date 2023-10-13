package client

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"log/slog"

	"github.com/elliotchance/sshtunnel"
	"golang.org/x/crypto/ssh"
)

type Client struct {
	signer ssh.Signer
	tunnel *sshtunnel.SSHTunnel
}

func NewClient() (*Client, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		return nil, err
	}

	return &Client{
		signer: signer,
	}, nil
}

type ConnectConfig struct {
	Server          string
	DestinationHost string
	DestinationPort string
	BindPort        string
}

func (c *Client) Start(cfg ConnectConfig) error {
	tunnel, err := sshtunnel.NewSSHTunnel(
		cfg.Server,
		ssh.PublicKeys(c.signer),
		fmt.Sprintf("%s:%s", cfg.DestinationHost, cfg.DestinationPort),
		"1234",
	)
	c.tunnel = tunnel

	if err != nil {
		return err
	}
	err = tunnel.Start()
	if err != nil {
		return err
	}
	return nil
}

func (c *Client) PublicKey() string {
	return base64.StdEncoding.EncodeToString(
		c.signer.PublicKey().Marshal(),
	)
}

func (c *Client) Shutdown() error {
	slog.Info("tunnel shutting down")
	c.tunnel.Close()
	return nil
}

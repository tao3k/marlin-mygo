// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package grpcwire

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/tao3k/marlin-mygo/control/transport"
)

var ErrInsecureRemoteForbidden = errors.New("grpcwire: plaintext gRPC forbidden on non-loopback remote address without TLS/mTLS")

// TLSConfig configures TLS 1.3 or mutual TLS (mTLS) for remote gRPC machines.
type TLSConfig struct {
	CAFile              string
	CertFile            string
	KeyFile             string
	ServerName          string
	AllowInsecureRemote bool
}

// IsLoopbackAddress reports whether host:port targets the local machine.
func IsLoopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// BuildTransportCredentials enforces TLS 1.3 / mTLS on non-loopback remote hosts
// while allowing loopback sidecar HTTP/2 when TLS is omitted.
func BuildTransportCredentials(mode transport.Mode, address string, cfg *TLSConfig) (credentials.TransportCredentials, error) {
	if cfg != nil && (cfg.CAFile != "" || cfg.CertFile != "" || cfg.KeyFile != "" || cfg.ServerName != "") {
		tlsCfg := &tls.Config{
			MinVersion: tls.VersionTLS13,
			ServerName: cfg.ServerName,
		}
		if cfg.CAFile != "" {
			pem, err := os.ReadFile(cfg.CAFile)
			if err != nil {
				return nil, fmt.Errorf("grpcwire: read CA %s: %w", cfg.CAFile, err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("grpcwire: invalid CA PEM in %s", cfg.CAFile)
			}
			tlsCfg.RootCAs = pool
		}
		if cfg.CertFile != "" || cfg.KeyFile != "" {
			if cfg.CertFile == "" || cfg.KeyFile == "" {
				return nil, fmt.Errorf("grpcwire: mTLS requires both CertFile and KeyFile")
			}
			cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("grpcwire: load client cert/key: %w", err)
			}
			tlsCfg.Certificates = []tls.Certificate{cert}
		}
		return credentials.NewTLS(tlsCfg), nil
	}

	if mode == transport.ModeRemoteGRPC && !IsLoopbackAddress(address) {
		if cfg == nil || !cfg.AllowInsecureRemote {
			return nil, fmt.Errorf("%w: %s", ErrInsecureRemoteForbidden, address)
		}
	}
	return insecure.NewCredentials(), nil
}

package health

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"
)

// TCP reports whether host:port accepts a connection.
func TCP(ctx context.Context, host string, port int, timeout time.Duration) error {
	dialer := net.Dialer{Timeout: timeout}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}

// HTTPResult is a completed HTTP response, including error statuses.
type HTTPResult struct {
	Status int
	URL    string
}

// Public requests url and treats any HTTP response as success.
func Public(ctx context.Context, rawURL string, timeout time.Duration) (HTTPResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return HTTPResult{}, err
	}
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return HTTPResult{}, err
	}
	defer resp.Body.Close()
	return HTTPResult{Status: resp.StatusCode, URL: rawURL}, nil
}

// LoopbackHTTPS requests https://hostname by dialing 127.0.0.1:443.
// This checks the local Caddy route when public DNS hairpinning is unavailable.
func LoopbackHTTPS(ctx context.Context, hostname string, timeout time.Duration) (HTTPResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+hostname+"/", nil)
	if err != nil {
		return HTTPResult{}, err
	}
	dialer := &net.Dialer{Timeout: timeout}
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, "127.0.0.1:443")
			},
			TLSClientConfig: &tls.Config{ServerName: hostname},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return HTTPResult{}, err
	}
	defer resp.Body.Close()
	return HTTPResult{Status: resp.StatusCode, URL: "https://" + hostname + "/"}, nil
}

// CertificateNames dials 127.0.0.1:443 with the given SNI and returns certificate DNS names.
func CertificateNames(ctx context.Context, serverName string, timeout time.Duration) ([]string, error) {
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", "127.0.0.1:443")
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	tlsConn := tls.Client(conn, &tls.Config{ServerName: serverName})
	if deadline, ok := ctx.Deadline(); ok {
		_ = tlsConn.SetDeadline(deadline)
	} else {
		_ = tlsConn.SetDeadline(time.Now().Add(timeout))
	}
	if err := tlsConn.Handshake(); err != nil {
		return nil, err
	}
	defer tlsConn.Close()
	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, fmt.Errorf("no certificate presented")
	}
	return state.PeerCertificates[0].DNSNames, nil
}

// LookupHost resolves host with a timeout.
func LookupHost(ctx context.Context, host string) ([]string, error) {
	resolver := net.Resolver{}
	return resolver.LookupHost(ctx, host)
}

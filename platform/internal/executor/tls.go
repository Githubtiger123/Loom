// Package executor authenticates the dedicated external ML adapter transport.
// A lease owner is only an execution fence; it is never a user identity token.
package executor

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/githubtiger123/loom/platform/internal/config"
)

type peerKey struct{}

type verifier struct {
	roots *x509.CertPool
	pins  map[[32]byte]struct{}
}

func newVerifier(cfg config.Executor) (*verifier, error) {
	if !cfg.Enabled || len(cfg.ClientCertSHA256) == 0 {
		return nil, errors.New("executor mTLS is disabled or has no client pins")
	}
	pem, err := os.ReadFile(cfg.ClientCA)
	if err != nil {
		return nil, errors.New("cannot read executor client CA")
	}
	v := &verifier{roots: x509.NewCertPool(), pins: make(map[[32]byte]struct{})}
	if !v.roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("executor client CA contains no certificates")
	}
	for _, pin := range cfg.ClientCertSHA256 {
		b, err := hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(pin), ":", ""))
		if err != nil || len(b) != sha256.Size {
			return nil, errors.New("executor client pin must be a SHA256 leaf certificate digest")
		}
		var digest [32]byte
		copy(digest[:], b)
		v.pins[digest] = struct{}{}
	}
	return v, nil
}

func (v *verifier) verify(cs *tls.ConnectionState) (string, error) {
	// VerifyConnection runs before HandshakeComplete is set by crypto/tls.
	if cs == nil || len(cs.PeerCertificates) == 0 || len(cs.VerifiedChains) == 0 {
		return "", errors.New("verified client certificate chain required")
	}
	leaf := cs.PeerCertificates[0]
	digest := sha256.Sum256(leaf.Raw)
	if _, ok := v.pins[digest]; !ok {
		return "", errors.New("executor leaf certificate is not pinned")
	}
	// Also verify against this listener's CA at request time. VerifiedChains from
	// another TLS listener or a stale resumed connection must not confer access.
	intermediates := x509.NewCertPool()
	for _, cert := range cs.PeerCertificates[1:] {
		intermediates.AddCert(cert)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: v.roots, Intermediates: intermediates,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return "", errors.New("executor client certificate verification failed")
	}
	return hex.EncodeToString(digest[:]), nil
}

// NewTLSConfig fails closed. ServeTLS("", "") can use the loaded server pair.
// Do not terminate this TLS connection in an HTTP proxy or expose its handler on
// the public API. Forwarded certificate headers are intentionally ignored.
func NewTLSConfig(cfg config.Executor) (*tls.Config, error) {
	v, err := newVerifier(cfg)
	if err != nil {
		return nil, err
	}
	pair, err := tls.LoadX509KeyPair(cfg.ServerCert, cfg.ServerKey)
	if err != nil {
		return nil, errors.New("cannot load executor server certificate/key")
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs: v.roots, Certificates: []tls.Certificate{pair},
		VerifyConnection: func(cs tls.ConnectionState) error { _, err := v.verify(&cs); return err },
	}, nil
}

// Authenticate never reads JWTs, cookies, identity headers or bearer tokens.
// Invalid configuration returns a permanently closed handler. Startup must first
// call NewTLSConfig so configuration failures are surfaced before listening.
func Authenticate(cfg config.Executor, next http.Handler) http.Handler {
	v, configErr := newVerifier(cfg)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if configErr != nil || next == nil {
			http.Error(w, "executor unavailable", http.StatusServiceUnavailable)
			return
		}
		if r.TLS == nil || !r.TLS.HandshakeComplete {
			http.Error(w, "executor certificate required", http.StatusForbidden)
			return
		}
		peer, err := v.verify(r.TLS)
		if err != nil {
			http.Error(w, "executor certificate required", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), peerKey{}, peer)))
	})
}

// PeerSHA256 is also checked by every internal handler, so accidentally wiring a
// machine handler through a user middleware does not grant reporting authority.
func PeerSHA256(r *http.Request) (string, bool) {
	peer, ok := r.Context().Value(peerKey{}).(string)
	if !ok || r.TLS == nil || !r.TLS.HandshakeComplete || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
		return "", false
	}
	digest := sha256.Sum256(r.TLS.PeerCertificates[0].Raw)
	return peer, peer == hex.EncodeToString(digest[:])
}

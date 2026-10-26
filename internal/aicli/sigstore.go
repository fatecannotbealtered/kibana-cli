// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

package aicli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
	tuffetcher "github.com/theupdateframework/go-tuf/v2/metadata/fetcher"
)

// Release signature verification (CLI-070, SEC-050): update verifies the Sigstore
// bundle of checksums.txt in process before it trusts any checksum. The trust root
// is refreshed through TUF, anchored on the root.json embedded in sigstore-go, so
// the refresh is authenticated, never trust on first use.

// ErrTrustRootUnavailable marks a verification that could not run because the
// Sigstore trust root could not be loaded (the TUF refresh failed or timed out).
// update reports it as E_NETWORK (retryable) instead of E_INTEGRITY. A custom
// UpdateConfig.Verify wraps it to get the same result.
var ErrTrustRootUnavailable = errors.New("the Sigstore trust root could not be loaded")

const (
	// githubActionsIssuer is the OIDC issuer of GitHub Actions signing certificates.
	githubActionsIssuer = "https://token.actions.githubusercontent.com"
	// trustRootTimeout bounds the TUF refresh of the Sigstore trust root.
	trustRootTimeout = 30 * time.Second
)

// fetchTrustedRoot is the TUF refresh; tests replace it so they never touch the network.
var fetchTrustedRoot = root.FetchTrustedRootWithOptions

// VerifySigstoreBundle verifies a Sigstore bundle (JSON) for artifact: the signing
// certificate's SAN must be exactly identity and its OIDC issuer exactly issuer;
// one signed certificate timestamp, one transparency log entry and one observer
// timestamp are required. It returns nil when the signature verifies, an error
// wrapping ErrTrustRootUnavailable when the trust root cannot be loaded, and any
// other error when the bundle does not verify.
func VerifySigstoreBundle(ctx context.Context, artifact, bundleJSON []byte, identity, issuer string) error {
	return verifySigstoreBundle(ctx, &http.Client{Timeout: trustRootTimeout}, artifact, bundleJSON, identity, issuer)
}

// verifySigstoreBundle is VerifySigstoreBundle with the HTTP client the TUF refresh
// uses (update passes its own: proxy, CA file and User-Agent, CLI-110).
func verifySigstoreBundle(ctx context.Context, client *http.Client, artifact, bundleJSON []byte, identity, issuer string) error {
	if identity == "" || issuer == "" {
		return errors.New("the signer identity and issuer are required")
	}
	var b bundle.Bundle
	if err := b.UnmarshalJSON(bundleJSON); err != nil {
		return fmt.Errorf("the signature bundle cannot be read: %w", err)
	}
	trusted, err := trustedRoot(ctx, client)
	if err != nil {
		return err
	}
	verifier, err := verify.NewVerifier(trusted,
		verify.WithSignedCertificateTimestamps(1),
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1),
	)
	if err != nil {
		return fmt.Errorf("the Sigstore verifier cannot be built: %w", err)
	}
	certID, err := verify.NewShortCertificateIdentity(issuer, "", identity, "")
	if err != nil {
		return fmt.Errorf("the certificate identity policy cannot be built: %w", err)
	}
	if _, err := verifier.Verify(&b, verify.NewPolicy(
		verify.WithArtifact(bytes.NewReader(artifact)),
		verify.WithCertificateIdentity(certID),
	)); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}
	return nil
}

// trustedRoot refreshes the Sigstore trusted root through TUF within
// trustRootTimeout. Any failure wraps ErrTrustRootUnavailable (and the context's
// error, so an interrupt stays recognisable).
func trustedRoot(ctx context.Context, client *http.Client) (*root.TrustedRoot, error) {
	refreshCtx, cancel := context.WithTimeout(ctx, trustRootTimeout)
	defer cancel()
	// DefaultOptions anchors TUF with sigstore-go's embedded root.json.
	opts := tuf.DefaultOptions()
	fetcher := tuffetcher.NewDefaultFetcher()
	fetcher.SetHTTPClient(client)
	opts.Fetcher = fetcher
	type result struct {
		tr  *root.TrustedRoot
		err error
	}
	done := make(chan result, 1)
	go func() {
		tr, err := fetchTrustedRoot(opts)
		done <- result{tr, err}
	}()
	select {
	case <-refreshCtx.Done():
		return nil, fmt.Errorf("%w: %w", ErrTrustRootUnavailable, refreshCtx.Err())
	case r := <-done:
		if r.err != nil {
			return nil, fmt.Errorf("%w: %w", ErrTrustRootUnavailable, r.err)
		}
		return r.tr, nil
	}
}

package cmd

import (
	"context"
	"fmt"
	"os"
	"regexp"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// errTrustRootUnavailable marks a failure to obtain the Sigstore trusted root
// (a TUF metadata refresh over the network). It is a transient/environmental
// fault, NOT a signature-integrity failure: the release may be perfectly valid
// but we could not reach or refresh the trust material. Callers map it to a
// retryable network error rather than the non-retryable E_INTEGRITY used for a
// signature or identity mismatch.
type errTrustRootUnavailable struct{ err error }

func (e *errTrustRootUnavailable) Error() string { return e.err.Error() }
func (e *errTrustRootUnavailable) Unwrap() error { return e.err }

// updateOIDCIssuer is the GitHub Actions OIDC issuer. The release workflow's
// Sigstore certificate must be issued for this exact issuer.
const updateOIDCIssuer = "https://token.actions.githubusercontent.com"

// updateSignerIdentityRegexp pins the certificate SAN to this repo's tagged
// release workflow. Forging a signature that matches would require minting a
// GitHub OIDC token for this exact workflow path on tag refs — i.e. compromising
// the repository's CI, not breaking the cryptography. Anchored ^...$ so it
// cannot be satisfied by a looser workflow whose identity merely contains it.
func updateSignerIdentityRegexp() string {
	return "^https://github\\.com/" + regexp.QuoteMeta(updateRepo) +
		"/\\.github/workflows/release\\.yml@refs/tags/v.*$"
}

// updateVerifySignature is the in-process Sigstore verification seam. Production
// verifies the bundle against the Sigstore TUF trust material (no external
// cosign, no user environment dependency); tests stub it to exercise the
// surrounding fail-closed control flow without a live OIDC-signed bundle.
var updateVerifySignature = verifySigstoreBundle

// verifySigstoreBundle verifies that artifactPath (checksums.txt) is covered by
// the Sigstore bundle at bundlePath, that the signing certificate's SAN matches
// sanRegex, that its issuer is GitHub Actions, and that the signature is logged
// in the transparency log.
//
// Trust root sourcing — honest description (CLI-SPEC §14 / SEC-SPEC §5):
//
//   - The TUF trust ANCHOR (root.json) is embedded in the sigstore-go library
//     and shipped inside this binary, so the chain is NOT trust-on-first-use:
//     tuf.DefaultOptions().Root is the library's embedded root.
//   - The trusted_root.json target itself is fetched/refreshed from the public
//     Sigstore TUF CDN. We pass WithForceCache() so a previously cached, still
//     valid trusted root is reused WITHOUT a network call, and bind the TUF
//     background context to ctx so a refresh is cancelled on SIGINT and bounded
//     by the command timeout. A refresh is still attempted when the cache is
//     missing or its metadata has expired.
//
// UNRESOLVED (carried in the update result): a first-ever verify on a machine
// with no TUF cache, or one whose cached metadata has expired, still performs a
// network refresh of trusted_root.json — sigstore-go v1.2.1 exposes no fully
// offline embedded trusted_root for the public-good instance. That refresh
// failing surfaces as a retryable network error (errTrustRootUnavailable), not
// the non-retryable E_INTEGRITY reserved for a signature/identity mismatch.
func verifySigstoreBundle(ctx context.Context, artifactPath, bundlePath, sanRegex string) error {
	b, err := bundle.LoadJSONFromPath(bundlePath)
	if err != nil {
		return fmt.Errorf("loading signature bundle: %w", err)
	}

	opts := tuf.DefaultOptions().WithForceCache().WithContext(ctx)
	trustedRoot, err := root.FetchTrustedRootWithOptions(opts)
	if err != nil {
		// Trust-material refresh failed (network/TUF), NOT a signature failure.
		return &errTrustRootUnavailable{fmt.Errorf("loading sigstore trust root: %w", err)}
	}

	sev, err := verify.NewVerifier(trustedRoot,
		verify.WithSignedCertificateTimestamps(1),
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1),
	)
	if err != nil {
		return fmt.Errorf("building sigstore verifier: %w", err)
	}

	certID, err := verify.NewShortCertificateIdentity(updateOIDCIssuer, "", "", sanRegex)
	if err != nil {
		return fmt.Errorf("building certificate identity policy: %w", err)
	}

	artifact, err := os.Open(artifactPath)
	if err != nil {
		return fmt.Errorf("opening signed artifact: %w", err)
	}
	defer func() { _ = artifact.Close() }()

	if _, err := sev.Verify(b, verify.NewPolicy(
		verify.WithArtifact(artifact),
		verify.WithCertificateIdentity(certID),
	)); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}
	return nil
}

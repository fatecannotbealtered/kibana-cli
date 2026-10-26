// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

package aicli

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// The binary install path and the Skill sync of update (CLI-070): everything before
// the swap happens in a temporary directory beside the executable (same
// filesystem), the swap is the one commit point, and the Skills are copied from the
// installed package into the Skill directories that already hold them.

var (
	// errWrite marks a local write failure (as opposed to bad archive content).
	errWrite = errors.New("local write failed")
	// errBadArchive marks an archive that cannot be read or holds no binary.
	errBadArchive = errors.New("the archive cannot be read or holds no binary")
)

// unsafeEntryError names an archive entry update refuses to install: an absolute
// path, a .. segment, a drive or stream name, a link, a device or other special
// file, a repeated name, or a file over 512 MiB.
type unsafeEntryError struct{ name string }

func (e *unsafeEntryError) Error() string { return "unsafe archive entry " + e.name }

func unsafeEntry(name string) error { return &unsafeEntryError{name: name} }

// writeTagger marks the errors of its writer with errWrite.
type writeTagger struct{ w io.Writer }

func (t writeTagger) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	if err != nil {
		err = fmt.Errorf("%w: %w", errWrite, err)
	}
	return n, err
}

// signatureNext is the next_action of an integrity failure of the signature.
const signatureNext = "Do not retry; tell the user the release failed signature verification."

func (u *updateRun) noSignedChecksums(version string) *Error {
	u.stage = "verify_signature"
	return u.fail("E_INTEGRITY", fmt.Sprintf("Release v%s has no signed checksums, so it was not installed.", version), signatureNext)
}

func (u *updateRun) checksumMismatch() *Error {
	u.stage = "verify_checksum"
	return u.fail("E_INTEGRITY", "The downloaded archive does not match its signed checksum, so it was not installed.",
		"Do not retry; tell the user the release archive failed its checksum.")
}

func (u *updateRun) cannotWrite(dir string) *Error {
	u.stage = "replace"
	return u.fail("E_IO", fmt.Sprintf("Cannot write to %s, where %s is installed.", dir, u.tool),
		fmt.Sprintf("Run the update from an account that can write there, or reinstall %s in a directory you own.", u.tool))
}

// platformAsset is the release archive for this machine: <Name>-<version>-<os>-<arch>
// (.zip on Windows, else .tar.gz); Windows on arm64 falls back to the amd64 build.
func (u *updateRun) platformAsset(rel *ghRelease, v Version) *ghAsset {
	goos, goarch := runtime.GOOS, runtime.GOARCH
	if goos != "linux" && goos != "darwin" && goos != "windows" {
		return nil
	}
	if goarch != "amd64" && goarch != "arm64" {
		return nil
	}
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	arches := []string{goarch}
	if goos == "windows" && goarch == "arm64" {
		arches = append(arches, "amd64")
	}
	for _, arch := range arches {
		if a := rel.asset(fmt.Sprintf("%s-%s-%s-%s.%s", u.tool, v.String(), goos, arch, ext)); a != nil {
			return a
		}
	}
	return nil
}

// verify checks the Sigstore bundle of checksums.txt (UpdateConfig.Verify, default
// VerifySigstoreBundle through the run's HTTP client).
func (u *updateRun) verify(checksums, bundle []byte, identity string) error {
	if u.cfg.Verify != nil {
		return u.cfg.Verify(u.ctx, checksums, bundle, identity, githubActionsIssuer)
	}
	client := &http.Client{Transport: u.client.Transport, Timeout: trustRootTimeout, CheckRedirect: u.client.CheckRedirect}
	return verifySigstoreBundle(u.ctx, client, checksums, bundle, identity, githubActionsIssuer)
}

// installBinary is the binary path: download, verify_signature, verify_checksum,
// replace (the swap), then the Skill sync.
func (u *updateRun) installBinary(exe string, rel *ghRelease, target Version) (map[string]any, *Error) {
	version := target.String()
	u.begin("download")
	asset := u.platformAsset(rel, target)
	if asset == nil {
		return nil, u.fail("E_UNSUPPORTED", fmt.Sprintf("Release v%s has no build for %s/%s.", version, runtime.GOOS, runtime.GOARCH),
			fmt.Sprintf("Install %s with the package-manager command in its README instead.", u.tool))
	}
	sums, bundle := rel.asset("checksums.txt"), rel.asset("checksums.txt.sigstore.json")
	if sums == nil || bundle == nil {
		return nil, u.noSignedChecksums(version)
	}
	// Every download is https (E13); a release that names another one cannot be
	// installed by update (the stage codes of download have no integrity failure).
	for _, a := range []*ghAsset{asset, sums, bundle} {
		if !httpsURL(a.URL) {
			return nil, u.fail("E_UNSUPPORTED", fmt.Sprintf("Release v%s names a download that is not https, so it was not installed.", version),
				fmt.Sprintf("Install %s with the package-manager command in its README instead, and report the release to the tool's maintainers.", u.tool))
		}
	}
	if exe == "" {
		return nil, u.fail("E_IO", fmt.Sprintf("%s cannot find its own executable, so it cannot update itself.", u.tool),
			fmt.Sprintf("Reinstall %s with the install command in its README.", u.tool))
	}
	dir := filepath.Dir(exe)
	tmp, err := os.MkdirTemp(dir, "."+u.tool+"-update-")
	if err != nil {
		return nil, u.cannotWrite(dir)
	}
	// After a failed rollback the new binary in tmp may be the only working copy
	// (E10): it is kept then.
	keepTmp := false
	defer func() {
		if !keepTmp {
			_ = os.RemoveAll(tmp)
		}
	}()

	ext := ".tar.gz"
	if strings.HasSuffix(asset.Name, ".zip") {
		ext = ".zip"
	}
	archive := filepath.Join(tmp, "release"+ext)
	f, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, u.cannotWrite(dir)
	}
	_, ferr := u.fetch(asset.URL, archiveLimit, writeTagger{f}, true)
	cerr := f.Close()
	var fe *fetchError
	switch {
	case ferr != nil && errors.Is(ferr, errWrite):
		return nil, u.cannotWrite(dir)
	case errors.As(ferr, &fe) && fe.tooLarge:
		return nil, u.checksumMismatch()
	case ferr != nil:
		return nil, u.netError(ferr, version)
	case cerr != nil:
		return nil, u.cannotWrite(dir)
	}
	signed := make([][]byte, 2)
	for i, a := range []*ghAsset{sums, bundle} {
		b, err := u.fetch(a.URL, signedFileLimit, nil, true)
		if errors.As(err, &fe) && fe.tooLarge {
			return nil, u.noSignedChecksums(version)
		}
		if err != nil {
			return nil, u.netError(err, version)
		}
		signed[i] = b
	}

	u.begin("verify_signature")
	identity := fmt.Sprintf("https://github.com/%s/.github/workflows/%s@refs/tags/v%s", u.cfg.Repo, u.cfg.Workflow, version)
	if err := u.verify(signed[0], signed[1], identity); err != nil {
		switch {
		case u.interrupted(err):
			return nil, u.interruptError()
		case errors.Is(err, ErrTrustRootUnavailable):
			return nil, u.fail("E_NETWORK",
				"Could not load the Sigstore trust root, so the release signature could not be checked; nothing was installed.",
				fmt.Sprintf("Check the network, then run `%s update` again.", u.tool))
		}
		return nil, u.fail("E_INTEGRITY",
			"The release signature did not verify, so the update was refused and the installed version is unchanged.", signatureNext)
	}
	u.sigStatus = "verified"

	u.begin("verify_checksum")
	want, ok := checksumFor(signed[0], asset.Name)
	got, err := fileSHA256(archive)
	if err != nil {
		return nil, u.cannotWrite(dir)
	}
	if !ok || got != want {
		return nil, u.checksumMismatch()
	}
	if u.ctx.Err() != nil {
		return nil, u.interruptError()
	}
	binName := u.tool
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}

	u.begin("replace")
	newBin, skills, err := extractRelease(archive, filepath.Join(tmp, "extracted"), binName)
	var unsafe *unsafeEntryError
	switch {
	case errors.As(err, &unsafe):
		return nil, u.fail("E_INTEGRITY", fmt.Sprintf("The release archive holds an unsafe entry `%s`, so it was not installed.",
			printable(unsafe.name, 80)), "Do not retry; tell the user the release archive is unsafe.")
	case errors.Is(err, errWrite):
		return nil, u.cannotWrite(dir)
	case err != nil:
		return nil, u.checksumMismatch()
	}
	observed, ran := u.observeVersion(newBin, false)
	if u.ctx.Err() != nil {
		return nil, u.interruptError()
	}
	if !ran {
		return nil, u.fail("E_IO", "The downloaded binary does not run on this machine, so it was not installed.",
			fmt.Sprintf("Install %s with the package-manager command in its README instead.", u.tool))
	}
	if observed == "" {
		return nil, u.fail("E_IO", "The downloaded binary did not report its version, so it was not installed.",
			fmt.Sprintf("Install %s with the package-manager command in its README instead.", u.tool))
	}
	if !sameVersion(observed, target) {
		return nil, u.fail("E_INTEGRITY", fmt.Sprintf("The downloaded binary reports version %s, not %s, so it was not installed.", observed, version),
			signatureNext)
	}
	if err := swapBinary(swapOS, newBin, exe, swapOps); err != nil {
		var rb *rollbackError
		if errors.As(err, &rb) {
			// The old binary could not be moved back (E10): the tool may be missing.
			u.lost, keepTmp = true, true
			return nil, u.fail("E_IO", fmt.Sprintf("The update failed and the old version could not be put back; %s may be missing.", u.tool),
				fmt.Sprintf("Rename %s back to %s, then run %s --version.", rb.old, exe, u.tool))
		}
		return nil, u.fail("E_IO", fmt.Sprintf("Replacing %s failed: %s; %s is still %s.", exe, failureReason(err), u.tool, u.current),
			fmt.Sprintf("Close other running copies of %s, then run `%s update` again.", u.tool, u.tool))
	}
	u.replaced, u.dispatched = true, true
	// <exe dir>/skills may be shared by several tools (E3): only the Skills this
	// release ships are replaced, one skills/<name> at a time, and only they are
	// synced. A release without skills/ leaves the directory alone.
	shipped := []string{}
	if skills != "" {
		u.begin("skill_sync")
		names, err := replaceSkillDirs(skills, filepath.Join(dir, "skills"))
		if err != nil {
			return nil, u.partial(err)
		}
		shipped = names
	}
	status, err := u.syncSkills(exe, version, shipped)
	if err != nil {
		return nil, u.partial(err)
	}
	return u.withSyncCommand(map[string]any{"status": "updated", "current_version": version, "target_version": version,
		"previous_version": u.previous, "update_available": false, "install_method": "binary",
		"signature_status": "verified", "signature_verified": true, "checksum_verified": true,
		"skill_sync_status": status, "binary_replaced": true}), nil
}

// failureReason is the OS's reason for a failed file operation, without the paths.
func failureReason(err error) string {
	reason := err.Error()
	switch e := err.(type) {
	case *os.LinkError:
		reason = e.Err.Error()
	case *os.PathError:
		reason = e.Err.Error()
	}
	return strings.TrimSuffix(strings.TrimSpace(reason), ".")
}

// checksumFor finds the one checksums.txt line "<64 lowercase hex>  <asset>".
func checksumFor(sums []byte, asset string) (string, bool) {
	digest, count := "", 0
	for _, line := range strings.Split(string(sums), "\n") {
		line = strings.TrimSuffix(line, "\r")
		i := strings.Index(line, "  ")
		if i < 0 || line[i+2:] != asset {
			continue
		}
		count++
		digest = line[:i]
	}
	if count != 1 || !sha256RE.MatchString(digest) {
		return "", false
	}
	return digest, true
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ---------------------------------------------------------------- extraction

// The caps of one archive's extraction (E13): entries of any kind, and the bytes
// of every file written. Variables only so that tests can lower them.
var (
	maxArchiveEntries       = 10000
	maxExtractedTotal int64 = 1 << 30
)

// swapOS and swapOps are the platform and file operations of the swap (tests
// replace them to reach the Windows rollback anywhere).
var (
	swapOS  = runtime.GOOS
	swapOps = fileOps{rename: renameRetry, remove: os.Remove}
)

// extractor writes <bin> and the skills/ tree of a release archive into dir.
type extractor struct {
	dir, bin            string
	foundBin, hasSkills bool
	entries             int
	written             int64
}

// extractRelease extracts the binary and skills/ from the archive root into dir.
// An unsafe entry (absolute path, .. segment, drive or stream name, link, device or
// other special file, repeated name, file over 512 MiB) is an unsafeEntryError; an
// archive that cannot be read or holds no binary is errBadArchive; a local write
// failure is errWrite. Other files (README, LICENSE) are skipped.
func extractRelease(archive, dir, bin string) (binPath, skillsDir string, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("%w: %w", errWrite, err)
	}
	x := &extractor{dir: dir, bin: bin}
	if strings.HasSuffix(archive, ".zip") {
		err = x.zip(archive)
	} else {
		err = x.tarGz(archive)
	}
	if err != nil {
		return "", "", err
	}
	if !x.foundBin {
		return "", "", errBadArchive
	}
	if x.hasSkills {
		skillsDir = filepath.Join(dir, "skills")
	}
	return filepath.Join(dir, bin), skillsDir, nil
}

func (x *extractor) zip(archive string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return errBadArchive
	}
	defer func() { _ = zr.Close() }()
	for _, f := range zr.File {
		mode := f.Mode()
		var open func() (io.ReadCloser, error)
		if mode.IsRegular() {
			open = f.Open
		} else if !mode.IsDir() {
			return unsafeEntry(f.Name)
		}
		if err := x.entry(f.Name, mode, open); err != nil {
			return err
		}
	}
	return nil
}

func (x *extractor) tarGz(archive string) error {
	file, err := os.Open(archive)
	if err != nil {
		return errBadArchive
	}
	defer func() { _ = file.Close() }()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return errBadArchive
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return errBadArchive
		}
		mode := fs.FileMode(hdr.Mode).Perm()
		var open func() (io.ReadCloser, error)
		switch hdr.Typeflag {
		case tar.TypeReg, '\x00':
			open = func() (io.ReadCloser, error) { return io.NopCloser(tr), nil }
		case tar.TypeDir:
			mode |= fs.ModeDir
		default: // links, devices, fifos and anything else
			return unsafeEntry(hdr.Name)
		}
		if err := x.entry(hdr.Name, mode, open); err != nil {
			return err
		}
	}
}

// cleanArchiveName validates an archive entry name: relative, no .. segment, no
// drive or stream (":"), no control characters. It returns the cleaned slash path.
func cleanArchiveName(name string) (string, bool) {
	n := strings.ReplaceAll(name, `\`, "/")
	if n == "" || strings.HasPrefix(n, "/") || strings.Contains(n, ":") ||
		strings.ContainsFunc(n, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", false
	}
	for _, seg := range strings.Split(n, "/") {
		if seg == ".." {
			return "", false
		}
	}
	return path.Clean(n), true
}

// entry places one archive entry; open is nil for a directory. An archive of more
// than 10,000 entries is refused at the entry past the cap.
func (x *extractor) entry(name string, mode fs.FileMode, open func() (io.ReadCloser, error)) error {
	x.entries++
	if x.entries > maxArchiveEntries {
		return unsafeEntry(name)
	}
	clean, ok := cleanArchiveName(name)
	if !ok {
		return unsafeEntry(name)
	}
	inSkills := clean == "skills" || strings.HasPrefix(clean, "skills/")
	switch {
	case clean == ".":
		return nil
	case open == nil && inSkills:
		x.hasSkills = true
		if err := os.MkdirAll(filepath.Join(x.dir, filepath.FromSlash(clean)), 0o755); err != nil {
			return fmt.Errorf("%w: %w", errWrite, err)
		}
		return nil
	case open == nil:
		return nil
	case clean == x.bin:
		x.foundBin = true
		return x.writeFile(name, clean, 0o755, open)
	case inSkills && clean != "skills":
		x.hasSkills = true
		perm := fs.FileMode(0o644)
		if mode&0o111 != 0 {
			perm = 0o755
		}
		return x.writeFile(name, clean, perm, open)
	}
	return nil
}

// writeFile writes one extracted file (at most 512 MiB, and at most 1 GiB for all
// files together; a repeated name is refused).
func (x *extractor) writeFile(name, clean string, perm fs.FileMode, open func() (io.ReadCloser, error)) error {
	target := filepath.Join(x.dir, filepath.FromSlash(clean))
	if _, err := os.Lstat(target); err == nil {
		return unsafeEntry(name)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("%w: %w", errWrite, err)
	}
	r, err := open()
	if err != nil {
		return errBadArchive
	}
	defer func() { _ = r.Close() }()
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return fmt.Errorf("%w: %w", errWrite, err)
	}
	limit := max(min(int64(extractedLimit), maxExtractedTotal-x.written), 0)
	n, err := io.Copy(writeTagger{f}, io.LimitReader(r, limit+1))
	if cerr := f.Close(); err == nil && cerr != nil {
		err = fmt.Errorf("%w: %w", errWrite, cerr)
	}
	x.written += n
	switch {
	case err != nil && errors.Is(err, errWrite):
		return err
	case err != nil:
		return errBadArchive
	case n > limit:
		return unsafeEntry(name)
	}
	return nil
}

// printable shows external text in a message: control characters dropped, at most
// n characters.
func printable(s string, n int) string {
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	return truncateRunes(clean, n)
}

// ---------------------------------------------------------------- the swap

// fileOps are the file operations of the swap (tests inject failures).
type fileOps struct {
	rename func(from, to string) error
	remove func(path string) error
}

// swapBinary moves newPath over exe, the one commit point of the binary path.
// POSIX: chmod 0755 and rename into place (same directory). Windows: the running
// exe cannot be replaced but can be renamed, so it is renamed aside to <exe>.old
// (<exe>.old-<n> when a stale .old cannot be removed) and the new file renamed in;
// if that fails the old file is renamed back.
func swapBinary(goos, newPath, exe string, ops fileOps) error {
	if goos != "windows" {
		if err := os.Chmod(newPath, 0o755); err != nil {
			return err
		}
		return ops.rename(newPath, exe)
	}
	old := exe + ".old"
	if _, err := os.Lstat(old); err == nil && ops.remove(old) != nil {
		old = ""
		for n := 1; n < 100 && old == ""; n++ {
			candidate := fmt.Sprintf("%s.old-%d", exe, n)
			if _, err := os.Lstat(candidate); errors.Is(err, fs.ErrNotExist) {
				old = candidate
			}
		}
		if old == "" {
			return errors.New("no free name to move the running binary aside")
		}
	}
	if err := ops.rename(exe, old); err != nil {
		return err
	}
	if err := ops.rename(newPath, exe); err != nil {
		if rerr := ops.rename(old, exe); rerr != nil {
			return &rollbackError{err: fmt.Errorf("%s (moving the old binary back also failed: %s; it is at %s)", failureReason(err), failureReason(rerr), old),
				old: old}
		}
		return err
	}
	return nil
}

// rollbackError is a failed swap whose rollback failed too: the running binary
// is no longer at its path, so the install state is unknown. old is where the old
// binary is now.
type rollbackError struct {
	err error
	old string
}

func (e *rollbackError) Error() string { return e.err.Error() }

// removeStaleBinaries removes the <exe>.old* files an earlier update left behind
// (best effort: a copy that is still running keeps its file).
func removeStaleBinaries(exe string) {
	if exe == "" {
		return
	}
	dir, base := filepath.Dir(exe), filepath.Base(exe)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if (n == base+".old" || strings.HasPrefix(n, base+".old-")) && e.Type().IsRegular() {
			_ = os.Remove(filepath.Join(dir, n))
		}
	}
}

// replaceDir puts the directory src at dst: the old dst is renamed aside, src
// renamed in, and the old one removed; if src cannot be renamed in, the old dst is
// restored.
func replaceDir(src, dst string) error {
	if _, err := os.Lstat(dst); errors.Is(err, fs.ErrNotExist) {
		return renameRetry(src, dst)
	}
	old := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".old-"+randomSuffix())
	if err := renameRetry(dst, old); err != nil {
		return err
	}
	if err := renameRetry(src, dst); err != nil {
		_ = renameRetry(old, dst)
		return err
	}
	_ = os.RemoveAll(old)
	return nil
}

func randomSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// replaceSkillDirs puts each Skill directory of a verified release's skills/ (src)
// at dst/<name>, one name at a time (E3): dst itself and the other names in it,
// which may be other tools' Skills in a shared directory, are left alone. It
// returns the names it placed.
func replaceSkillDirs(src, dst string) ([]string, error) {
	if info, err := os.Lstat(dst); err == nil && (info.Mode()&fs.ModeSymlink != 0 || info.Mode()&fs.ModeIrregular != 0 || !info.IsDir()) {
		return nil, fmt.Errorf("%s is not a directory", dst)
	} else if errors.Is(err, fs.ErrNotExist) {
		if err := os.Mkdir(dst, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if err := replaceDir(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return names, err
		}
		names = append(names, e.Name())
	}
	return names, nil
}

var skillRequires = regexp.MustCompile(`(?m)^[ \t]+requires\.skills:[ \t]*"?([^"\s#]+)"?`)

// ownSkills is the tool's own Skills in a binary install's <exe dir>/skills, which
// other tools in the same directory may share (E3): the entry Skill named after the
// tool and the domain Skills whose metadata.requires.skills names it (SKL-010). It
// is nil, every Skill of the source, for the package-manager installs, whose
// package holds only this tool's Skills.
func (u *updateRun) ownSkills(exe string) []string {
	if u.method != "binary" {
		return nil
	}
	names := []string{}
	src := u.skillSource(exe)
	if src == "" {
		return names
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return names
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if e.Name() == u.tool {
			names = append(names, e.Name())
			continue
		}
		b, err := readSmallFile(filepath.Join(src, e.Name(), "SKILL.md"), 256<<10)
		if err != nil {
			continue
		}
		if fm := skillFrontmatter.FindStringSubmatch(string(b)); fm != nil {
			if m := skillRequires.FindStringSubmatch(fm[1]); m != nil && m[1] == u.tool {
				names = append(names, e.Name())
			}
		}
	}
	return names
}

// ---------------------------------------------------------------- Skill sync

// skillSource is the installed package's skills/ directory: beside the binary for
// a binary install, in the npm wrapper package for npm, pnpm and yarn, none for
// the other package managers.
func (u *updateRun) skillSource(exe string) string {
	switch u.method {
	case "binary":
		if exe != "" {
			return filepath.Join(filepath.Dir(exe), "skills")
		}
	case "npm", "pnpm", "yarn":
		if root := npmPackageRoot(exe, u.cfg.NPMPackage); root != "" {
			return filepath.Join(root, "skills")
		}
	}
	return ""
}

// npmPackageRoot walks up from the executable to the directory whose package.json
// names pkg.
func npmPackageRoot(exe, pkg string) string {
	if exe == "" || pkg == "" {
		return ""
	}
	for dir := filepath.Dir(exe); ; {
		if b, err := readSmallFile(filepath.Join(dir, "package.json"), 1<<20); err == nil {
			var p struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(b, &p) == nil && p.Name == pkg {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// syncSkills copies each Skill of the installed package into the Skill directories
// that hold it (stage skill_sync): a real directory is replaced by a copy, a link
// is left alone, a missing one is not created. names limits the sync to those
// Skills (nil: every Skill of the source, E3). Links are never followed: a source
// Skill or a skills/ directory that is a link is not copied. It returns skipped (no
// source, or no Skill installed anywhere), not_needed (every installed copy already
// has this version), synced (something was copied), or failed with the error.
func (u *updateRun) syncSkills(exe, version string, names []string) (string, error) {
	u.begin("skill_sync")
	src := u.skillSource(exe)
	if src == "" {
		return "skipped", nil
	}
	if info, err := os.Lstat(src); err != nil || info.Mode()&fs.ModeSymlink != 0 || info.Mode()&fs.ModeIrregular != 0 || !info.IsDir() {
		return "skipped", nil
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return "skipped", nil
	}
	var skills []string
	for _, e := range entries {
		if names != nil && !contains(names, e.Name()) {
			continue
		}
		if info, err := os.Lstat(filepath.Join(src, e.Name(), "SKILL.md")); e.IsDir() && err == nil && info.Mode().IsRegular() {
			skills = append(skills, e.Name())
		}
	}
	roots := u.a.SkillDirs
	if roots == nil {
		roots = defaultSkillDirs()
	}
	type installed struct {
		skill, path   string
		link, current bool
	}
	var found []installed
	for _, s := range skills {
		for _, root := range roots {
			p := filepath.Join(root, s)
			info, err := os.Lstat(p)
			if err != nil {
				continue
			}
			link := info.Mode()&fs.ModeSymlink != 0 || info.Mode()&fs.ModeIrregular != 0
			if !link && !info.IsDir() {
				continue
			}
			current := false
			if b, err := readSmallFile(filepath.Join(p, "SKILL.md"), 256<<10); err == nil {
				v, _ := skillVersions(string(b))
				current = sameVersionText(v, version)
			}
			found = append(found, installed{s, p, link, current})
		}
	}
	if len(found) == 0 {
		return "skipped", nil
	}
	allCurrent := true
	for _, f := range found {
		allCurrent = allCurrent && f.current
	}
	if allCurrent {
		return "not_needed", nil
	}
	copied := 0
	for _, f := range found {
		if f.link || f.current {
			continue
		}
		if err := u.replaceSkill(filepath.Join(src, f.skill), f.path); err != nil {
			return "failed", err
		}
		copied++
	}
	if copied == 0 {
		return "skipped", nil
	}
	return "synced", nil
}

// sameVersionText reports two equal semantic versions.
func sameVersionText(a, b string) bool {
	va, err1 := ParseVersion(a)
	vb, err2 := ParseVersion(b)
	return err1 == nil && err2 == nil && CompareVersions(va, vb) == 0
}

// replaceSkill replaces the installed Skill directory dst with a copy of src: copy
// to <root>/.<skill>.new-<random>, rename the old one aside, rename the copy in,
// remove the old one.
func (u *updateRun) replaceSkill(src, dst string) error {
	tmp := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".new-"+randomSuffix())
	copyDir := copyTree
	if u.a.skillCopy != nil {
		copyDir = u.a.skillCopy
	}
	if err := copyDir(src, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := replaceDir(tmp, dst); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	return nil
}

// copyTree copies the directories and regular files under src to dst (links and
// other special files are skipped).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case !d.Type().IsRegular():
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()|0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	})
}

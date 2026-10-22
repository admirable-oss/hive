package updater

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Kinds of installation the updater can replace.
const (
	// KindApp is a macOS .app bundle, replaced as a whole.
	KindApp = "app"
	// KindAppImage is a Linux AppImage file, replaced as a whole.
	KindAppImage = "appimage"
	// KindInstaller is a Windows installation directory, updated by running
	// the release's installer silently into it.
	KindInstaller = "installer"
)

// Target is what an update replaces: Path is the bundle, the AppImage or
// the installation directory, and Exe what to start afterwards.
type Target struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
	Exe  string `json:"exe"`
}

// ErrUnmanaged means the app was not installed in a way it can update
// itself: a .deb or .rpm (its package manager updates it), Homebrew, or a
// development build outside a bundle.
var ErrUnmanaged = errors.New("this installation cannot update itself")

// Locate finds what an update replaces, from the running executable.
func Locate(goos, exe string, getenv func(string) string) (Target, error) {
	switch goos {
	case "darwin":
		// …/Hive.app/Contents/MacOS/hive-desktop
		macos := filepath.Dir(exe)
		contents := filepath.Dir(macos)
		bundle := filepath.Dir(contents)
		if filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" || !strings.HasSuffix(bundle, ".app") {
			return Target{}, fmt.Errorf("%w: %s is not inside an .app bundle", ErrUnmanaged, exe)
		}
		if strings.Contains(bundle, "/Caskroom/") {
			return Target{}, fmt.Errorf("%w: Homebrew installed it; run `brew upgrade --cask hive-desktop`", ErrUnmanaged)
		}
		return Target{Kind: KindApp, Path: bundle, Exe: exe}, nil
	case "windows":
		return Target{Kind: KindInstaller, Path: filepath.Dir(exe), Exe: exe}, nil
	default:
		if img := getenv("APPIMAGE"); img != "" {
			return Target{Kind: KindAppImage, Path: img, Exe: img}, nil
		}
		return Target{}, fmt.Errorf("%w: it was installed by a package (update it with your package manager) or is not an AppImage", ErrUnmanaged)
	}
}

// Previous is where the version an update replaced is kept.
func Previous(t Target) string { return t.Path + ".old" }

// Stage makes a downloaded package ready to swap in, beside the target (on
// the same volume, so the swap is a rename), and returns what to swap in.
func Stage(t Target, pkg string) (string, error) {
	switch t.Kind {
	case KindApp:
		dir := t.Path + ".staged"
		if err := os.RemoveAll(dir); err != nil {
			return "", err
		}
		if err := unzip(pkg, dir); err != nil {
			_ = os.RemoveAll(dir)
			return "", fmt.Errorf("unpack %s: %w", filepath.Base(pkg), err)
		}
		apps, _ := filepath.Glob(filepath.Join(dir, "*.app"))
		if len(apps) != 1 {
			_ = os.RemoveAll(dir)
			return "", fmt.Errorf("%s holds no single .app", filepath.Base(pkg))
		}
		rel, _ := filepath.Rel(t.Path, t.Exe)
		if info, err := os.Stat(filepath.Join(apps[0], rel)); err != nil || info.IsDir() {
			_ = os.RemoveAll(dir)
			return "", fmt.Errorf("the new app has no %s", rel)
		}
		return apps[0], nil
	case KindAppImage:
		staged := t.Path + ".staged"
		if err := copyFile(pkg, staged, 0o755); err != nil {
			return "", err
		}
		return staged, nil
	case KindInstaller:
		return pkg, nil
	}
	return "", fmt.Errorf("unknown installation kind %q", t.Kind)
}

// swap puts staged in place of the target, keeping the replaced version at
// Previous(t). The target is never missing for longer than between two
// renames; if the second fails, the old one is put back.
func swap(t Target, staged string) error {
	prev := Previous(t)
	if err := os.RemoveAll(prev); err != nil {
		return err
	}
	switch t.Kind {
	case KindApp, KindAppImage:
		if err := os.Rename(t.Path, prev); err != nil {
			return fmt.Errorf("move %s aside (the app needs to be where you can write): %w", t.Path, err)
		}
		if err := os.Rename(staged, t.Path); err != nil {
			_ = os.Rename(prev, t.Path)
			return err
		}
		if t.Kind == KindApp {
			_ = os.RemoveAll(filepath.Dir(staged)) // the emptied staging directory
		}
		return nil
	case KindInstaller:
		if err := copyDir(t.Path, prev); err != nil {
			return fmt.Errorf("keep the installed version: %w", err)
		}
		dir, err := filepath.Abs(t.Path)
		if err != nil {
			return err
		}
		// NSIS: /S is silent; /D= must come last and is not quoted.
		if out, err := exec.Command(staged, "/S", "/D="+dir).CombinedOutput(); err != nil { //nolint:noctx // the installer runs to its end
			_ = restore(t)
			return fmt.Errorf("the installer failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	return fmt.Errorf("unknown installation kind %q", t.Kind)
}

// restore puts the previous version back in place of the target.
func restore(t Target) error {
	prev := Previous(t)
	if _, err := os.Stat(prev); err != nil {
		return fmt.Errorf("no previous version to go back to: %w", err)
	}
	failed := t.Path + ".failed"
	_ = os.RemoveAll(failed)
	if _, err := os.Stat(t.Path); err == nil {
		if err := os.Rename(t.Path, failed); err != nil {
			return err
		}
	}
	if err := os.Rename(prev, t.Path); err != nil {
		_ = os.Rename(failed, t.Path)
		return err
	}
	_ = os.RemoveAll(failed)
	return nil
}

// unzip extracts archive into dir, refusing entries that would land
// outside it and keeping file modes and symlinks (bundles have both).
func unzip(archive, dir string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		// AppleDouble files (._name, __MACOSX/) are how a zip made on macOS
		// carries extended attributes. They are not part of the bundle:
		// written as files, they break its code signature.
		if base := f.Name[strings.LastIndex(f.Name, "/")+1:]; strings.HasPrefix(base, "._") || strings.HasPrefix(f.Name, "__MACOSX/") {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(f.Name))
		if path != root && !strings.HasPrefix(path, root+string(filepath.Separator)) {
			return fmt.Errorf("%q would land outside the archive's folder", f.Name)
		}
		mode := f.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
		case mode&fs.ModeSymlink != 0:
			rc, err := f.Open()
			if err != nil {
				return err
			}
			link, err := io.ReadAll(io.LimitReader(rc, 4096))
			_ = rc.Close()
			if err != nil {
				return err
			}
			to := filepath.Join(filepath.Dir(path), string(link))
			if filepath.IsAbs(string(link)) || (to != root && !strings.HasPrefix(to, root+string(filepath.Separator))) {
				return fmt.Errorf("%q links outside the archive's folder", f.Name)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(string(link), path); err != nil {
				return err
			}
		default:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			rc, err := f.Open()
			if err != nil {
				return err
			}
			err = writeFile(path, rc, mode.Perm()|0o600)
			_ = rc.Close()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func writeFile(path string, r io.Reader, perm fs.FileMode) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, io.LimitReader(r, maxPackage))
	return errors.Join(err, out.Close())
}

func copyFile(from, to string, perm fs.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	return errors.Join(writeFile(to, in, perm), os.Chmod(to, perm))
}

// copyDir copies a directory tree (an installation, to keep it).
func copyDir(from, to string) error {
	return filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(path, dst, info.Mode().Perm())
	})
}

package lsp

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

// filePathFromURI converts a file: document URI to a local path. A URI with
// any other scheme names no file on disk, so the caller publishes nothing for
// it.
func filePathFromURI(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || !strings.EqualFold(u.Scheme, "file") {
		return "", false
	}
	p := u.Path
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		// A non-local host is a UNC share, which only Windows can name.
		if runtime.GOOS != "windows" {
			return "", false
		}
		p = "//" + u.Host + p
	}
	if p == "" {
		return "", false
	}
	if runtime.GOOS == "windows" {
		// file:///C:/x parses with a leading slash before the drive letter.
		if len(p) >= 3 && p[0] == '/' && isDriveLetter(p[1]) && p[2] == ':' {
			p = p[1:]
		}
		p = strings.ReplaceAll(p, "/", "\\")
	}
	return filepath.Clean(p), true
}

// isDriveLetter reports whether b is an ASCII drive letter.
func isDriveLetter(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// pathURI renders a local path as a file: document URI. Notes can point into
// another file, so their location needs a URI that may not be open.
func pathURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	p := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" && len(p) >= 2 && isDriveLetter(p[0]) && p[1] == ':' {
		p = "/" + p
	}
	return "file://" + (&url.URL{Path: p}).EscapedPath()
}

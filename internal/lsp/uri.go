package lsp

// Document URIs, in both directions.
//
// The client sends file:///d%3A/code/x.ggm; the compiler wants a filesystem
// path. The two differ in three ways at once -- percent-encoding, forward
// slashes, and the leading slash before a Windows drive letter -- so this is
// done with net/url rather than by trimming strings.
//
// Percent-encoding matters here more than in most projects: the reference data
// pack has files and objectives with Chinese names, and the client encodes
// them. Trimming instead of decoding would produce a path that does not exist
// and diagnostics attached to a document the editor never opened.

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

// pathFromURI converts a document URI to a filesystem path.
func pathFromURI(raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" {
		return "", false
	}
	p := u.Path
	if u.Host != "" {
		// file://server/share/x -- a UNC path.
		p = "//" + u.Host + p
	}
	return toLocalPath(p), true
}

// uriFromPath converts a filesystem path to a document URI.
func uriFromPath(path string) DocumentURI {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p}
	return DocumentURI(u.String())
}

// toLocalPath turns a URL path into a path the host accepts.
func toLocalPath(p string) string {
	if runtime.GOOS != "windows" {
		return p
	}
	// /d:/code/x.ggm -> d:/code/x.ggm -> D:\code\x.ggm
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

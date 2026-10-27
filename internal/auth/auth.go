// Package auth manages the agent token (docs/adr/0002-authentication.md).
//
// The token lives in a file that is also a curl header file, so agents that
// wait in the background can send it without ever seeing it:
//
//	curl -s -H @<state dir>/auth 'http://127.0.0.1:8720/api/events/next?…'
package auth

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Header is the marker every API request carries. It is not a secret: a
// browser cannot send it cross-origin without a CORS preflight, and vitrine
// never approves one.
const Header = "X-Vitrine"

// FileName is the name of the token file in the state directory.
const FileName = "auth"

const prefix = "vitrine_"

// LoadOrCreate returns the token stored in path, creating the file with a new
// random token if it does not exist. The file is readable only by its owner:
// mode 0600 on Unix; on Windows the state directory below %LOCALAPPDATA%
// inherits a profile ACL that admits only the user, SYSTEM and administrators.
func LoadOrCreate(path string) (string, error) {
	if tok, err := load(path); !errors.Is(err, fs.ErrNotExist) {
		return tok, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	// Write a complete temporary file and link it into place: the link fails
	// if the file exists, so concurrent processes never see a partial token
	// and all end up with the same one.
	tok := New()
	tmp, err := os.CreateTemp(filepath.Dir(path), ".auth-*") // mode 0600
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(HeaderFile(tok)); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Link(tmp.Name(), path); errors.Is(err, fs.ErrExist) {
		return load(path) // another vitrine process was faster
	} else if err != nil {
		return "", err
	}
	return tok, nil
}

// New returns a random token.
func New() string {
	b := make([]byte, 32)
	rand.Read(b) // never fails, see crypto/rand
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

// HeaderFile is the content of the token file: one header per line, as curl
// expects for -H @file.
func HeaderFile(token string) string {
	return "Authorization: Bearer " + token + "\n" + Header + ": 1\n"
}

func load(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), "Authorization") {
			if tok, ok := strings.CutPrefix(strings.TrimSpace(v), "Bearer "); ok && tok != "" {
				return strings.TrimSpace(tok), nil
			}
		}
	}
	return "", fmt.Errorf("%s has no Authorization: Bearer line; delete it to create a new token", path)
}

// Bearer returns the token of an Authorization: Bearer header.
func Bearer(r *http.Request) string {
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return strings.TrimSpace(tok)
}

// Equal compares tokens in constant time. An empty token never matches.
func Equal(got, want string) bool {
	return got != "" && want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

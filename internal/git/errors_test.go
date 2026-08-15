package git

import (
	"errors"
	"testing"
)

func TestCommandErrorClassifiesDeletedUpstreamRef(t *testing.T) {
	for _, stderr := range []string{
		"fatal: couldn't find remote ref refs/heads/feature\n",
		"fatal: could not find remote ref refs/heads/feature\n",
	} {
		result := commandResult{stderr: []byte(stderr), err: errors.New("exit status 128"), exitCode: 128}
		err := commandError("fetch", "network", result)
		if !IsKind(err, ErrorMissingUpstreamRef) {
			t.Fatalf("commandError(%q) = %v, want ErrorMissingUpstreamRef", stderr, err)
		}
	}
}

func TestCommandErrorKeepsNetworkAndAuthenticationDistinct(t *testing.T) {
	network := commandError("fetch", "network", commandResult{stderr: []byte("fatal: Could not resolve host: github.com"), err: errors.New("exit status 128"), exitCode: 128})
	if !IsKind(network, ErrorNetwork) {
		t.Fatalf("network error = %v", network)
	}
	auth := commandError("fetch", "network", commandResult{stderr: []byte("fatal: Authentication failed for 'https://example.com'"), err: errors.New("exit status 128"), exitCode: 128})
	if !IsKind(auth, ErrorAuthentication) {
		t.Fatalf("authentication error = %v", auth)
	}
}

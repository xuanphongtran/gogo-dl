package docs

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestGeneratedAsyncAPIMatchesSource(t *testing.T) {
	source, err := asyncAPI.ReadFile("asyncapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := asyncAPI.ReadFile("asyncapi/source.sha256")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(digest)) != fmt.Sprintf("%x", sha256.Sum256(source)) {
		t.Fatal("AsyncAPI HTML is stale; run nvm use 22 and make docs-asyncapi")
	}
	served, err := asyncAPI.ReadFile("asyncapi/asyncapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if string(served) != string(source) {
		t.Fatal("served AsyncAPI contract is stale; run make docs-asyncapi")
	}
}

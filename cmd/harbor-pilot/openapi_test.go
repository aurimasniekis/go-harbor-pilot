package main

import (
	"bytes"
	"os"
	"regexp"
	"testing"
)

// The committed api/openapi.yaml is the published contract (consumed by the
// web UI types and the Terraform provider). It must match the code.
func TestOpenAPIIsCurrent(t *testing.T) {
	got, err := openAPIDocument()
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("api/openapi.yaml is stale; run `make gen-api`")
	}
}

func TestEveryOperationDeclaresPermission(t *testing.T) {
	doc, err := openAPIDocument()
	if err != nil {
		t.Fatal(err)
	}
	ops := bytes.Count(doc, []byte("operationId:"))
	perms := bytes.Count(doc, []byte("x-permission:"))
	if ops == 0 || ops != perms {
		t.Fatalf("operations=%d x-permission=%d", ops, perms)
	}
}

func TestSecretsNotAcceptedAsArguments(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	// Flags naming a secret's source (-file, -stdin, -env) are fine; a flag
	// taking the secret value itself is not.
	re := regexp.MustCompile(`--(password|token|secret|api-key|api-token)(\s|=|$)`)
	if m := re.Find(out.Bytes()); m != nil {
		t.Fatalf("usage advertises secret flag %s", m)
	}
}

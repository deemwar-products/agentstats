package jobs

import (
	"bytes"
	"encoding/base64"
	"log"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	// Obviously fake tokens of every GitHub shape.
	fakes := []string{
		"ghp_FAKE0000000000000000000000000000000000",
		"gho_FAKE0000000000000000000000000000000000",
		"ghu_FAKE0000000000000000000000000000000000",
		"ghs_FAKE0000000000000000000000000000000000",
		"ghr_FAKE0000000000000000000000000000000000",
		"github_pat_11FAKE00000_FAKEFAKEFAKEFAKEFAKE",
	}
	for _, tok := range fakes {
		got := Redact("fatal: auth failed for " + tok + " at https://github.com/o/r.git")
		if strings.Contains(got, tok) || strings.Contains(got, "FAKE0000") {
			t.Errorf("not redacted: %s", got)
		}
		if !strings.Contains(got, "https://github.com/o/r.git") {
			t.Errorf("over-redacted: %s", got)
		}
	}
	got := Redact(`http.extraHeader=Authorization: Bearer opaque-token-value and more`)
	if strings.Contains(got, "opaque-token-value") || !strings.Contains(got, "Authorization: Bearer REDACTED") {
		t.Errorf("bearer header not redacted: %s", got)
	}
	if s := "ghost_town gh_ ghx_abc github"; Redact(s) != s {
		t.Errorf("false positive: %s", Redact(s))
	}
}

func TestRedactWriterWithLogger(t *testing.T) {
	var buf bytes.Buffer
	l := log.New(RedactWriter{W: &buf}, "", 0)
	l.Printf("clone failed: %s", "ghu_FAKE0000000000000000000000000000000000")
	if strings.Contains(buf.String(), "ghu_FAKE") {
		t.Fatalf("logger leaked token: %s", buf.String())
	}
}

func TestTokenArgsOnlyInExtraHeader(t *testing.T) {
	tok := "ghu_FAKE0000000000000000000000000000000000"
	args := tokenArgs(tok)
	want := "http.extraHeader=Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+tok))
	if len(args) != 2 || args[0] != "-c" || args[1] != want {
		t.Fatalf("tokenArgs = %v", args)
	}
	if tokenArgs("") != nil {
		t.Fatal("empty token must add no args")
	}
	if err := bareClone("https://x-access-token:"+tok+"@github.com/o/r.git", t.TempDir()+"/r.git", ""); err == nil {
		t.Fatal("clone URL with embedded credentials must be refused")
	}
}

func TestRedactBasicHeader(t *testing.T) {
	got := Redact(`http.extraHeader=Authorization: Basic eC1hY2Nlc3MtdG9rZW46ZmFrZQ== tail`)
	if strings.Contains(got, "eC1hY2Nlc3MtdG9rZW46ZmFrZQ") || !strings.Contains(got, "Authorization: Basic REDACTED") {
		t.Fatalf("basic header not redacted: %q", got)
	}
}
